package automation

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

func matchCondition(event ClipboardEvent, c Condition) bool {
	if len(c.All) > 0 {
		for _, child := range c.All {
			if !matchCondition(event, child) {
				return false
			}
		}
		return true
	}
	if len(c.Any) > 0 {
		for _, child := range c.Any {
			if matchCondition(event, child) {
				return true
			}
		}
		return false
	}
	if c.Not != nil {
		return !matchCondition(event, *c.Not)
	}

	got, ok := fieldValue(event, c.Field)
	if !ok {
		return false
	}
	want := fmt.Sprint(c.Value)
	switch strings.ToLower(strings.TrimSpace(c.Op)) {
	case "eq":
		return got == want
	case "ne":
		return got != want
	case "contains":
		return strings.Contains(strings.ToLower(got), strings.ToLower(want))
	case "starts_with":
		return strings.HasPrefix(strings.ToLower(got), strings.ToLower(want))
	case "ends_with":
		return strings.HasSuffix(strings.ToLower(got), strings.ToLower(want))
	case "regex":
		re, err := regexp.Compile(want)
		return err == nil && re.MatchString(got)
	case "in":
		switch list := c.Value.(type) {
		case []any:
			for _, item := range list {
				if got == fmt.Sprint(item) {
					return true
				}
			}
		case []string:
			for _, item := range list {
				if got == item {
					return true
				}
			}
		}
		return false
	case "gt", "gte", "lt", "lte":
		left, lErr := strconv.ParseFloat(got, 64)
		right, rErr := strconv.ParseFloat(want, 64)
		if lErr != nil || rErr != nil {
			return false
		}
		switch strings.ToLower(c.Op) {
		case "gt":
			return left > right
		case "gte":
			return left >= right
		case "lt":
			return left < right
		case "lte":
			return left <= right
		}
	}
	return false
}

func fieldValue(event ClipboardEvent, field string) (string, bool) {
	switch strings.TrimSpace(field) {
	case "username":
		return event.Username, true
	case "transport":
		return event.Transport, true
	case "source.ip":
		return event.Source.IP, true
	case "clipboard.type":
		return event.Clipboard.Type, true
	case "clipboard.payload_size":
		return strconv.FormatInt(event.Clipboard.PayloadSize, 10), true
	case "clipboard.filename":
		return event.Clipboard.FileName, true
	case "clipboard.text_preview":
		return event.Clipboard.TextPreview, true
	case "clipboard.is_encrypted":
		return strconv.FormatBool(event.Clipboard.IsEncrypted), true
	default:
		return "", false
	}
}
