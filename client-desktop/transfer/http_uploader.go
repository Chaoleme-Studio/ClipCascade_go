package transfer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

func UploadFile(client *http.Client, serverURL string, path string, onProgress func(sent, total int64)) (*UploadResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}

	bodyPrefix, bodySuffix, contentType, err := multipartEnvelope(filepath.Base(path))
	if err != nil {
		_ = file.Close()
		return nil, err
	}

	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	// 失败路径必须关闭 pipe 读端，否则泵 goroutine 与文件句柄永久泄漏。
	closePipe := func(err error) {
		_ = pr.CloseWithError(err)
	}
	go func() {
		defer close(errCh)
		defer file.Close()
		defer pw.Close()

		if _, err := pw.Write(bodyPrefix); err != nil {
			errCh <- err
			_ = pw.CloseWithError(err)
			return
		}
		reader := &progressReader{reader: file, total: info.Size(), onProgress: onProgress}
		if _, err := io.Copy(pw, reader); err != nil {
			errCh <- err
			_ = pw.CloseWithError(err)
			return
		}
		if _, err := pw.Write(bodySuffix); err != nil {
			errCh <- err
			_ = pw.CloseWithError(err)
			return
		}
		errCh <- nil
	}()

	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/files/upload", pr)
	if err != nil {
		closePipe(err)
		return nil, err
	}
	req.ContentLength = int64(len(bodyPrefix)) + info.Size() + int64(len(bodySuffix))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-File-Size", strconv.FormatInt(info.Size(), 10))
	resp, err := client.Do(req)
	if err != nil {
		// 关闭读端解除泵 goroutine 阻塞，避免 goroutine + 文件句柄泄漏
		closePipe(err)
		return nil, err
	}
	defer resp.Body.Close()
	if err := <-errCh; err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upload failed: %s", string(data))
	}
	var result UploadResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func multipartEnvelope(fileName string) ([]byte, []byte, string, error) {
	var header bytes.Buffer
	writer := multipart.NewWriter(&header)
	if _, err := writer.CreateFormFile("file", fileName); err != nil {
		return nil, nil, "", err
	}
	contentType := writer.FormDataContentType()
	if err := writer.Close(); err != nil {
		return nil, nil, "", err
	}

	full := header.Bytes()
	boundaryMarker := []byte("\r\n--" + writer.Boundary() + "--\r\n")
	idx := bytes.LastIndex(full, boundaryMarker)
	if idx < 0 {
		return nil, nil, "", fmt.Errorf("failed to build multipart envelope")
	}

	prefix := append([]byte(nil), full[:idx]...)
	suffix := append([]byte(nil), full[idx:]...)
	return prefix, suffix, contentType, nil
}

type progressReader struct {
	reader     io.Reader
	total      int64
	sent       int64
	onProgress func(sent, total int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.sent += int64(n)
		if r.onProgress != nil {
			r.onProgress(r.sent, r.total)
		}
	}
	return n, err
}
