package service

import (
	"context"
	"strings"
	"testing"
)

func TestWeatherServiceQueryEmptyLocation(t *testing.T) {
	ws := NewWeatherService()
	_, err := ws.Query(context.Background(), "", nil)
	if err == nil {
		t.Fatal("expected error for empty location")
	}
	if !strings.Contains(err.Error(), "location") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWeatherServiceQueryIntegration 需要联网访问 wttr.in，默认跳过。
func TestWeatherServiceQueryIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ws := NewWeatherService()
	res, err := ws.Query(context.Background(), "Chongqing", nil)
	if err != nil {
		t.Fatalf("query weather failed: %v", err)
	}
	if res.Text == "" {
		t.Fatal("expected non-empty weather text")
	}
	for _, want := range []string{"当前天气", "温度", "湿度"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("output missing %q: %s", want, res.Text)
		}
	}
}
