package tui

import (
	"strings"
	"testing"
)

func TestValidatePollMarkupNoPoll(t *testing.T) {
	if err := validatePollMarkup("hello world"); err != nil {
		t.Fatalf("expected no error for plain body, got %v", err)
	}
}

func TestValidatePollMarkupValid(t *testing.T) {
	cases := []string{
		"[poll]\nQuestion?\n- Option 1\n- Option 2\n[/poll]",
		"[POLL]\nQuestion?\n- Option 1\n- Option 2\n[/poll]",
		"[poll]\nQuestion?\n- Option 1\n- Option 2\n[/POLL]",
	}
	for _, body := range cases {
		if err := validatePollMarkup(body); err != nil {
			t.Fatalf("expected valid poll %q, got %v", body, err)
		}
	}
}

func TestValidatePollMarkupInvalid(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantSub string
	}{
		{
			name:    "bad_opening_tag",
			body:    "[poll expires=]\nQuestion?\n- Option 1\n- Option 2\n[/poll]",
			wantSub: "poll tag is malformed",
		},
		{
			name:    "invalid_expires",
			body:    "[poll expires=badtime]\nQuestion?\n- Option 1\n- Option 2\n[/poll]",
			wantSub: "poll closing time is invalid",
		},
		{
			name:    "missing_close_tag",
			body:    "[poll]\nQuestion?\n- Option 1\n- Option 2",
			wantSub: "missing a closing",
		},
		{
			name:    "missing_question",
			body:    "[poll]\n- Option 1\n- Option 2\n[/poll]",
			wantSub: "add a question",
		},
		{
			name:    "not_enough_options",
			body:    "[poll]\nQuestion?\n- Option 1\n[/poll]",
			wantSub: "at least two options",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePollMarkup(c.body)
			if err == nil {
				t.Fatalf("expected invalid poll for case %s", c.name)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("expected error containing %q, got %q", c.wantSub, err)
			}
		})
	}
}
