package test

import (
	dpTask "github.com/WnJee/gorig-om/src/deploy/task"
	"github.com/WnJee/gorig/utils/logger"
	"testing"
)

func TestSaveTask(t *testing.T) {
	ctx := logger.NewCtx()

	if e := dpTask.Task.SaveConfig(ctx, dpTask.TaskOptions{
		Repo:   "git@github.com:WnJee/gorig.git",
		Branch: "test",
	}); e != nil {
		t.Errorf("Error: %v", e)
		return
	}
}
