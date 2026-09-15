package health

import (
	"testing"

	"gpt-load/internal/execution"
)

func TestDecisionShouldRetry(t *testing.T) {
	tests := []struct {
		retry RetryDirective
		want  bool
	}{
		{retry: RetryNone, want: false},
		{retry: RetryRefreshCredential, want: true},
		{retry: RetryNextCandidate, want: true},
		{retry: RetryDirective("unknown"), want: false},
		{retry: "", want: false},
	}
	for _, test := range tests {
		if got := (Decision{Retry: test.retry}).ShouldRetry(); got != test.want {
			t.Fatalf("Decision{Retry:%q}.ShouldRetry() = %t, want %t", test.retry, got, test.want)
		}
	}
}

func TestDecisionIndicatesUpstreamCapacityPressure(t *testing.T) {
	tests := []struct {
		name     string
		decision Decision
		want     bool
	}{
		{
			name: "upstream rate limit",
			decision: Decision{
				Origin:   execution.ErrorOriginUpstream,
				Category: FailureCategoryRateLimited,
				RuleID:   RuleID("candidate.rate_limited"),
			},
			want: true,
		},
		{
			name: "upstream had no room before processing",
			decision: Decision{
				Origin:   execution.ErrorOriginUpstream,
				Category: FailureCategoryAmbiguous,
				RuleID:   ruleTransientCapacity,
			},
			want: true,
		},
		{
			// The credential is wrong, not the shard; moving the conversation
			// elsewhere would burn its upstream cache for nothing.
			name: "upstream rejected the credential",
			decision: Decision{
				Origin:   execution.ErrorOriginUpstream,
				Category: FailureCategoryInvalidKey,
				RuleID:   RuleID("candidate.invalid_key"),
			},
			want: false,
		},
		{
			name: "the client sent a bad request",
			decision: Decision{
				Origin:   execution.ErrorOriginClient,
				Category: FailureCategoryClientError,
				RuleID:   RuleID("request.client_error"),
			},
			want: false,
		},
		{
			// Same rule, but the pressure was never observed upstream.
			name: "internal error carrying the capacity rule",
			decision: Decision{
				Origin:   execution.ErrorOriginInternal,
				Category: FailureCategoryRateLimited,
				RuleID:   ruleTransientCapacity,
			},
			want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.decision.IndicatesUpstreamCapacityPressure(); got != test.want {
				t.Fatalf("IndicatesUpstreamCapacityPressure() = %t, want %t", got, test.want)
			}
		})
	}
}
