package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/clipcascade/desktop/config"
	"github.com/clipcascade/pkg/netutil"
	"github.com/grandcat/zeroconf"
)

func discoverServers(parent context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	resolver, err := zeroconf.NewResolver(zeroconf.SelectIfaces(netutil.UsableLANInterfaces()))
	if err != nil {
		return nil, err
	}
	entries := make(chan *zeroconf.ServiceEntry, 32)
	if err := resolver.Browse(ctx, "_clipcascade._tcp", "local.", entries); err != nil {
		return nil, err
	}
	return collectDiscoveredServers(ctx, entries, 350*time.Millisecond)
}

// 找到地址后留出短暂收集窗口，不再无条件等满整个五秒超时。
func collectDiscoveredServers(ctx context.Context, entries <-chan *zeroconf.ServiceEntry, settle time.Duration) ([]string, error) {
	var found []string
	seen := make(map[string]bool)
	var ready <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	finish := func() ([]string, error) {
		if len(found) == 0 {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("no server discovered: %w", err)
			}
			return nil, fmt.Errorf("no server discovered")
		}
		netutil.SortDiscoveredURLs(found)
		return found, nil
	}
	for {
		select {
		case <-ctx.Done():
			return finish()
		case <-ready:
			return finish()
		case entry, ok := <-entries:
			if !ok {
				return finish()
			}
			if entry == nil || entry.Port <= 0 || entry.Port > 65535 {
				continue
			}
			for _, ip := range entry.AddrIPv4 {
				if !netutil.IsUsableLANIPv4(ip) {
					continue
				}
				address := fmt.Sprintf("http://%s:%d", ip, entry.Port)
				if !seen[address] {
					seen[address] = true
					found = append(found, address)
					if timer == nil {
						timer = time.NewTimer(settle)
						ready = timer.C
					}
				}
			}
		}
	}
}

type loginResult struct {
	url     string
	cookies []*http.Cookie
	err     error
}

func (a *Application) findServer(parent context.Context) (string, []*http.Cookie, error) {
	return a.raceServers(parent, []string{a.cfg.ServerURL}, streamDiscoveredServers)
}

// 连接阶段持续消费发现结果：新地址一到就尝试，较晚出现的服务也不会被首个结果截断。
func streamDiscoveredServers(parent context.Context, found func(string)) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	resolver, err := zeroconf.NewResolver(zeroconf.SelectIfaces(netutil.UsableLANInterfaces()))
	if err != nil {
		return
	}
	entries := make(chan *zeroconf.ServiceEntry, 32)
	if err := resolver.Browse(ctx, "_clipcascade._tcp", "local.", entries); err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-entries:
			if !ok {
				return
			}
			if entry == nil || entry.Port <= 0 || entry.Port > 65535 {
				continue
			}
			for _, ip := range entry.AddrIPv4 {
				if netutil.IsUsableLANIPv4(ip) {
					found(fmt.Sprintf("http://%s:%d", ip, entry.Port))
				}
			}
		}
	}
}

// 每轮只建立一个最终连接；候选地址并发登录，避免旧 IP 超时挡住可用 IP。
// 独立 cookie jar 防止同一主机不同端口的并发登录覆盖彼此的 session。
func (a *Application) raceServers(parent context.Context, known []string, discover func(context.Context, func(string))) (string, []*http.Cookie, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	results := make(chan loginResult)
	go func() {
		var workers sync.WaitGroup
		semaphore := make(chan struct{}, 4)
		seen := make(map[string]bool)
		submit := func(raw string) {
			address := config.NormalizeServerURL(raw)
			if address == "" || seen[address] {
				return
			}
			seen[address] = true
			workers.Add(1)
			go func() {
				defer workers.Done()
				select {
				case semaphore <- struct{}{}:
					defer func() { <-semaphore }()
				case <-ctx.Done():
					return
				}
				cookies, err := a.loginAt(ctx, address)
				select {
				case results <- loginResult{address, cookies, err}:
				case <-ctx.Done():
				}
			}()
		}
		for _, address := range known {
			submit(address)
		}
		discover(ctx, submit)
		workers.Wait()
		close(results)
	}()
	var lastErr error = fmt.Errorf("no server available")
	for {
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case result, ok := <-results:
			if !ok {
				return "", nil, lastErr
			}
			if result.err != nil {
				lastErr = result.err
				continue
			}
			u, _ := url.Parse(result.url)
			if a.httpClient.Jar != nil {
				a.httpClient.Jar.SetCookies(u, result.cookies)
			}
			return result.url, result.cookies, nil
		}
	}
}

func (a *Application) loginAt(parent context.Context, address string) ([]*http.Cookie, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	jar, _ := cookiejar.New(nil)
	client := *a.httpClient
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	form := url.Values{"username": {a.cfg.Username}, "password": {a.cfg.Password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		return nil, fmt.Errorf("login returned HTTP %d", resp.StatusCode)
	}
	if strings.Contains(resp.Header.Get("Location"), "error") {
		return nil, fmt.Errorf("invalid credentials")
	}
	u, _ := url.Parse(address)
	cookies := jar.Cookies(u)
	if len(cookies) == 0 {
		return nil, fmt.Errorf("no session cookie received")
	}
	return cookies, nil
}
