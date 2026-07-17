package application

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
)

// PriorityWriter updates one auth file through CPA's Management fields API.
type PriorityWriter interface {
	SetPriority(exactFileName string, priority int) error
}

type ManagementPriorityWriter struct {
	endpoint string
	key      string
	client   *http.Client
}

func NewManagementPriorityWriter(baseURL, key string, timeout time.Duration) (PriorityWriter, error) {
	baseURL = strings.TrimSpace(baseURL)
	key = strings.TrimSpace(key)
	if baseURL == "" || key == "" {
		return nil, nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid CPA_GROK_MANAGEMENT_BASE_URL %q", baseURL)
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/v0/management/auth-files/fields"):
	case strings.HasSuffix(path, "/v0/management"):
		path += "/auth-files/fields"
	default:
		path += "/v0/management/auth-files/fields"
	}
	parsed.Path = path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &ManagementPriorityWriter{
		endpoint: parsed.String(), key: key, client: &http.Client{Timeout: timeout},
	}, nil
}

func isNilPriorityWriter(writer PriorityWriter) bool {
	if writer == nil {
		return true
	}
	value := reflect.ValueOf(writer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (writer *ManagementPriorityWriter) SetPriority(exactFileName string, priority int) error {
	body, err := json.Marshal(struct {
		Name     string `json:"name"`
		Priority int    `json:"priority"`
	}{Name: exactFileName, Priority: priority})
	if err != nil {
		return fmt.Errorf("encode Management fields request: %w", err)
	}
	request, err := http.NewRequest(http.MethodPatch, writer.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Management fields request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+writer.key)
	request.Header.Set("Content-Type", "application/json")
	response, err := writer.client.Do(request)
	if err != nil {
		return fmt.Errorf("Management fields PATCH: %w", err)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(responseBody))
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return fmt.Errorf("Management fields PATCH HTTP %d: %s", response.StatusCode, message)
	}
	return nil
}
