package errstat

import (
	"github.com/gin-gonic/gin"
	"github.com/jom-io/gorig/apix"
	"github.com/jom-io/gorig/cache"
	"github.com/jom-io/gorig/global/consts"
)

func TimeRange(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	start, _ := apix.GetParamInt64(ctx, "start", apix.Force)
	end, _ := apix.GetParamInt64(ctx, "end", apix.Force)
	unit, _ := apix.GetParamStr(ctx, "unit", "day")
	filter, _ := apix.GetParamArray[ErrType](ctx, "filter", apix.Force)

	resUsage, err := S().TimeRange(ctx, start, end, cache.Granularity(unit), filter...)
	apix.HandleData(ctx, consts.CurdSelectFailCode, resUsage, err)
}

func Top(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	start, _ := apix.GetParamInt64(ctx, "start", apix.Force)
	end, _ := apix.GetParamInt64(ctx, "end", apix.Force)
	limit, _ := apix.GetParamInt64(ctx, "limit", apix.Force)
	filter, _ := apix.GetParamArray[ErrType](ctx, "filter", apix.Force)

	data, e := S().TopSignatures(ctx, start, end, filter, limit)
	apix.HandleData(ctx, consts.CurdSelectFailCode, data, e)
}
