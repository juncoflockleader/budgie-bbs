package logmodel

import (
	"encoding/base64"
	"sort"
	"strings"
)

func BrokerSubject(prefix string, partition Partition) string {
	partition = partition.Normalize()
	return prefix + "." + EncodeSubjectToken(partition.Kind) + "." + EncodeSubjectToken(partition.Key)
}

func BrokerSubjectWildcard(prefix string) string {
	return prefix + ".>"
}

func ParseBrokerSubject(prefix, subject string) (Partition, bool) {
	rest, ok := strings.CutPrefix(subject, prefix+".")
	if !ok {
		return Partition{}, false
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 2 {
		return Partition{}, false
	}
	kind, err := DecodeSubjectToken(parts[0])
	if err != nil {
		return Partition{}, false
	}
	key, err := DecodeSubjectToken(parts[1])
	if err != nil {
		return Partition{}, false
	}
	return Partition{Kind: kind, Key: key}.Normalize(), true
}

func EncodeSubjectToken(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func DecodeSubjectToken(value string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func NormalizeScopes(scopes []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, raw := range scopes {
		scope := strings.TrimSpace(raw)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	sort.Strings(out)
	return out
}
