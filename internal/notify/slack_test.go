package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSlackNotify(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	notifier := NewSlackNotifier(server.URL)
	err := notifier.Notify(context.Background(), "test message")
	if err != nil {
		t.Fatalf("Notify failed: %v", err)
	}
}

func TestSlackNotifyEmptyURL(t *testing.T) {
	notifier := NewSlackNotifier("")
	err := notifier.Notify(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error with empty webhook URL")
	}
}
