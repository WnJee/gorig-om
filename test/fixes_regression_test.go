package test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jom-io/gorig-om/src/deploy"
	deployEnv "github.com/jom-io/gorig-om/src/deploy/env"
	dpTask "github.com/jom-io/gorig-om/src/deploy/task"
	"github.com/jom-io/gorig-om/src/omuser"
	"github.com/jom-io/gorig-om/src/stat/apistat"
	"github.com/jom-io/gorig-om/src/stat/errstat"
	"github.com/jom-io/gorig-om/src/stat/gorstat"
	"github.com/jom-io/gorig-om/src/stat/memstat"
	"github.com/jom-io/gorig/global/variable"
	"github.com/jom-io/gorig/utils/logger"
	"golang.org/x/crypto/bcrypt"
)

func TestFixLoginTimeWindowTolerance(t *testing.T) {
	variable.OMKey = "test-om-secret-key"
	gin.SetMode(gin.TestMode)

	now := time.Now().Unix() / 10
	currentPwd := fmt.Sprintf("%d%s", now, variable.OMKey)
	prevPwd := fmt.Sprintf("%d%s", now-1, variable.OMKey)
	invalidPwd := fmt.Sprintf("%d%s", now-2, variable.OMKey)

	// Hash current password
	hashCurrent, err := bcrypt.GenerateFromPassword([]byte(currentPwd), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash current password: %v", err)
	}

	// Hash previous password
	hashPrev, err := bcrypt.GenerateFromPassword([]byte(prevPwd), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash prev password: %v", err)
	}

	// Hash invalid/expired password
	hashInvalid, err := bcrypt.GenerateFromPassword([]byte(invalidPwd), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash invalid password: %v", err)
	}

	w1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(w1)
	c1.Request = httptest.NewRequest("POST", "/om/login", nil)
	c1.Request.RemoteAddr = "127.0.0.1:12345"

	// Test login with current password
	tokensCurrent, errCurrent := omuser.LoginByPwd(c1, string(hashCurrent))
	if errCurrent != nil {
		t.Fatalf("expected login success with current window, got error: %v", errCurrent)
	}
	if tokensCurrent == nil {
		t.Fatalf("expected tokens for current window login")
	}

	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest("POST", "/om/login", nil)
	c2.Request.RemoteAddr = "127.0.0.2:12345"

	// Test login with previous window password
	tokensPrev, errPrev := omuser.LoginByPwd(c2, string(hashPrev))
	if errPrev != nil {
		t.Fatalf("expected login success with previous window, got error: %v", errPrev)
	}
	if tokensPrev == nil {
		t.Fatalf("expected tokens for previous window login")
	}

	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	c3.Request = httptest.NewRequest("POST", "/om/login", nil)
	c3.Request.RemoteAddr = "127.0.0.3:12345"

	// Test login with expired/invalid password
	_, errInvalid := omuser.LoginByPwd(c3, string(hashInvalid))
	if errInvalid == nil {
		t.Fatalf("expected login failure with expired window")
	}
}

func TestFixRunCommandErrorHandling(t *testing.T) {
	ctx := context.Background()
	opts := deploy.DefOpts()

	// Running a failing command should always return a non-nil error
	_, err := deploy.RunCommand(ctx, "false", opts)
	if err == nil {
		t.Fatalf("expected RunCommand to return error for 'false' command")
	}
}

func TestFixGoEnvGetDefault(t *testing.T) {
	ctx := logger.NewCtx()
	envs := deployEnv.Env.GoEnvGet(ctx)
	if envs == nil {
		t.Fatalf("expected GoEnvGet to return non-nil default environments")
	}
	if len(envs) == 0 {
		t.Fatalf("expected at least one default env")
	}
}

func TestFixStatSingletonsConcurrency(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			_ = apistat.S()
		}()
		go func() {
			defer wg.Done()
			_ = errstat.S()
		}()
		go func() {
			defer wg.Done()
			_ = gorstat.S()
		}()
		go func() {
			defer wg.Done()
			_ = memstat.S()
		}()
	}
	wg.Wait()
}

func TestFixTaskStartWithHash(t *testing.T) {
	ctx := logger.NewCtx()
	_ = dpTask.Task.SaveConfig(ctx, dpTask.TaskOptions{
		Repo:   "git@github.com-jom:jom-io/gorig.git",
		Branch: "main",
	})
	err := dpTask.Task.Start(ctx, false, "abc1234")
	if err != nil {
		t.Fatalf("expected task Start to succeed, got: %v", err)
	}
}
