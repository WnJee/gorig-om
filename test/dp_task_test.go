package test

import (
	dpTask "github.com/WnJee/gorig-om/src/deploy/task"
	"github.com/jom-io/gorig/utils/logger"
	"testing"
)

func TestSaveTask(t *testing.T) {
	ctx := logger.NewCtx()

	if e := dpTask.Task.SaveConfig(ctx, dpTask.TaskOptions{
		Repo:   "git@github.com-jom:jom-io/gorig.git",
		Branch: "test",
	}); e != nil {
		t.Errorf("Error: %v", e)
		return
	}
}
