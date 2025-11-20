package monitor

import "testing"

func TestEvaluateStatusWithoutSuccessContains(t *testing.T) {
	t.Parallel()

	if got := evaluateStatus(1, []byte("anything"), ""); got != 1 {
		t.Fatalf("expected 1 when success_contains is empty, got %d", got)
	}
}

func TestEvaluateStatusWithMatchingContent(t *testing.T) {
	t.Parallel()

	body := []byte(`{"ok":true,"message":"pong"}`)
	if got := evaluateStatus(1, body, "pong"); got != 1 {
		t.Fatalf("expected 1 when body contains keyword, got %d", got)
	}
}

func TestEvaluateStatusWithNonMatchingContent(t *testing.T) {
	t.Parallel()

	body := []byte(`{"ok":false,"message":"error"}`)
	if got := evaluateStatus(1, body, "pong"); got != 0 {
		t.Fatalf("expected 0 when body does not contain keyword, got %d", got)
	}
}
