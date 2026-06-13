package notifier

import (
	"reflect"
	"testing"
)

func TestCommandArgs(t *testing.T) {
	got := CommandArgs("/app/data/notify-provider.yaml", "")
	want := []string{"-provider", "telegram", "-provider-config", "/app/data/notify-provider.yaml", "-bulk", "-silent"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CommandArgs=%v, want %v", got, want)
	}
}

func TestCommandArgsWithProxy(t *testing.T) {
	got := CommandArgs("/app/data/notify-provider.yaml", "socks5://user:pass@example.com:1080")
	want := []string{"-provider", "telegram", "-provider-config", "/app/data/notify-provider.yaml", "-bulk", "-silent", "-proxy", "socks5://user:pass@example.com:1080"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CommandArgs=%v, want %v", got, want)
	}
}
