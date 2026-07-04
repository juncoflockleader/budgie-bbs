package loadutil

import (
	"context"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/loadmodel"
)

func TestBody(t *testing.T) {
	if got := Body(0); got != "load" {
		t.Fatalf("zero body = %q", got)
	}
	if got := Body(5); len(got) != 5 || got != "parti" {
		t.Fatalf("sized body = %q", got)
	}
}

func TestSafeID(t *testing.T) {
	if got := SafeID(" Load-ID.01 "); got != "load_id_01" {
		t.Fatalf("safe id = %q", got)
	}
	if got := SafeID(" - "); got != "load" {
		t.Fatalf("empty safe id = %q", got)
	}
}

func TestRunCommandLogLoadSubmitStage(t *testing.T) {
	stage, err := RunCommandLogLoadSubmitStage(context.Background(), loadmodel.CommandLogLoadStage{Commands: 5}, 2, "submit", func(_ int, job int) string {
		switch job {
		case 1, 3:
			return "duplicate failure"
		case 4:
			return "other failure"
		default:
			return ""
		}
	})
	if err == nil || !strings.Contains(err.Error(), "submit failed 3/5 commands") {
		t.Fatalf("submit stage err = %v, want failure count", err)
	}
	if stage.Succeeded != 2 || stage.Failed != 3 || stage.DurationMS < 0 {
		t.Fatalf("submit stage = %+v, want 2 succeeded and 3 failed", stage)
	}
	if len(stage.SampleErrorText) != 2 {
		t.Fatalf("sample errors = %+v, want de-duplicated failures", stage.SampleErrorText)
	}
}

func TestRunCommandLogLoadSubmitStageEmpty(t *testing.T) {
	stage, err := RunCommandLogLoadSubmitStage(context.Background(), loadmodel.CommandLogLoadStage{}, 0, "submit", func(int, int) string {
		t.Fatal("submit should not be called for empty stage")
		return ""
	})
	if err != nil {
		t.Fatalf("empty submit stage err = %v", err)
	}
	if stage.Commands != 0 || stage.Succeeded != 0 || stage.Failed != 0 {
		t.Fatalf("empty submit stage = %+v", stage)
	}
}
