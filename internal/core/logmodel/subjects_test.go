package logmodel

import (
	"reflect"
	"testing"
)

func TestNormalizeScopesTrimsDedupesAndSorts(t *testing.T) {
	got := NormalizeScopes([]string{
		" thread:thr_1 ",
		"",
		"board:general",
		"thread:thr_1",
		"  chat:lobby",
	})
	want := []string{"board:general", "chat:lobby", "thread:thr_1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeScopes = %#v, want %#v", got, want)
	}
}
