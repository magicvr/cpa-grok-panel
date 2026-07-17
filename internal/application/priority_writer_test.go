package application_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/application"
)

func TestManagementPriorityWriterPatchesExactFile(t *testing.T) {
	var gotName string
	var gotPriority int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPatch || request.URL.Path != "/v0/management/auth-files/fields" {
			t.Fatalf("request=%s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("authorization=%q", got)
		}
		var body struct {
			Name     string `json:"name"`
			Priority int    `json:"priority"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotName, gotPriority = body.Name, body.Priority
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	writer, err := application.NewManagementPriorityWriter(server.URL, "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.SetPriority("xai-exact.json", -123); err != nil {
		t.Fatal(err)
	}
	if gotName != "xai-exact.json" || gotPriority != -123 {
		t.Fatalf("name=%q priority=%d", gotName, gotPriority)
	}
}

func TestManagementPriorityWriterRequiresBothEnvironmentValues(t *testing.T) {
	writer, err := application.NewManagementPriorityWriter("http://127.0.0.1:8317", "", time.Second)
	if err != nil || writer != nil {
		t.Fatalf("writer=%v err=%v", writer, err)
	}
	if _, err := application.NewManagementPriorityWriter("not-a-url", "secret", time.Second); err == nil {
		t.Fatal("invalid base URL was accepted")
	}
}
