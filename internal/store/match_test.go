package store

import "testing"

func TestMatchTopic(t *testing.T) {
	cases := []struct {
		pattern, topic string
		want           bool
	}{
		{"orders.created", "orders.created", true},
		{"orders.created", "orders.deleted", false},
		{"orders.*", "orders.created", true},
		{"orders.*", "orders.items.added", true}, // prefix glob, not segment glob (documented)
		{"orders.*", "orders", false},
		{"orders.*", "payments.created", false},
		{"*", "anything.at.all", true},
		{"", "orders.created", false},
	}
	for _, c := range cases {
		if got := MatchTopic(c.pattern, c.topic); got != c.want {
			t.Errorf("MatchTopic(%q, %q) = %v, want %v", c.pattern, c.topic, got, c.want)
		}
	}
}
