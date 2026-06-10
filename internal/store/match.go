package store

import "strings"

// MatchTopic implements P0 topic matching: exact match, or a trailing-*
// prefix glob ("orders.*" matches "orders.created" and deeper). "*" alone
// matches everything. This is deliberately simple; segment-aware patterns
// are out of P0 scope.
func MatchTopic(pattern, topic string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, ".*"); ok {
		return strings.HasPrefix(topic, prefix+".")
	}
	return pattern == topic
}
