package diag

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jom-io/gorig/apix"
	"github.com/jom-io/gorig/global/consts"
	"github.com/jom-io/gorig/utils/errors"
)

func GetGoroutines(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	res, err := DumpAndClusterGoroutines()
	if err != nil {
		apix.HandleData(ctx, consts.CurdSelectFailCode, nil, errors.Verify(err.Error()))
		return
	}
	apix.HandleData(ctx, consts.CurdSelectFailCode, res, nil)
}

func GetRawGoroutines(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	raw := RawGoroutinesDump()
	ctx.Header("Content-Type", "text/plain; charset=utf-8")
	ctx.String(http.StatusOK, raw)
}

func GetCPUProfile(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	seconds, _ := apix.GetParamInt(ctx, "seconds", apix.NotForce, 10)
	data, err := CaptureCPUProfile(ctx.Request.Context(), seconds)
	if err != nil {
		apix.HandleData(ctx, consts.CurdSelectFailCode, nil, errors.Verify(err.Error()))
		return
	}

	filename := fmt.Sprintf("cpu_%s_%ds.pprof", time.Now().Format("20060102_150405"), seconds)
	ctx.Header("Content-Type", "application/octet-stream")
	ctx.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	ctx.Data(http.StatusOK, "application/octet-stream", data)
}

func GetHeapProfile(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	data, err := CaptureHeapProfile()
	if err != nil {
		apix.HandleData(ctx, consts.CurdSelectFailCode, nil, errors.Verify(err.Error()))
		return
	}

	filename := fmt.Sprintf("heap_%s.pprof", time.Now().Format("20060102_150405"))
	ctx.Header("Content-Type", "application/octet-stream")
	ctx.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	ctx.Data(http.StatusOK, "application/octet-stream", data)
}
