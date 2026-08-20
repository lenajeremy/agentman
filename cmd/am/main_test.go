package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/source"
)

func TestHistoryPageLimitBoundary(t *testing.T) {
	for _, limit := range []int{-1, 0, source.MaxPageMessages + 1, int(^uint(0) >> 1)} {
		if err := source.ValidatePageLimit(limit); err == nil {
			t.Errorf("limit %d was accepted", limit)
		}
	}
	for _, limit := range []int{1, 30, source.MaxPageMessages} {
		if err := source.ValidatePageLimit(limit); err != nil {
			t.Errorf("limit %d was rejected: %v", limit, err)
		}
	}
}

func TestRunHistoryRejectsUnsafeLimitsBeforeDiscovery(t *testing.T) {
	for _, limit := range []int{-1, 0, source.MaxPageMessages + 1, int(^uint(0) >> 1)} {
		err := runHistory(context.Background(), []string{
			"claude:any-session", "-limit", fmt.Sprint(limit),
		})
		if err == nil || !strings.Contains(err.Error(), "message limit must be between") {
			t.Errorf("limit %d returned %v", limit, err)
		}
	}
}
