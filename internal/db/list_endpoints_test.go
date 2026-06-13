package db

import (
	"strings"
	"testing"
)

func TestEndpointWhereNeedsAttentionState(t *testing.T) {
	where, args := endpointWhere(ListEndpointsParams{State: "needs_attention"})

	if !strings.Contains(where, "state IN ('warning', 'offline')") {
		t.Fatalf("where=%q, want grouped warning/offline state filter", where)
	}
	if len(args) != 0 {
		t.Fatalf("args=%v, want no state args for grouped filter", args)
	}
}
