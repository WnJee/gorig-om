package gorstat

import (
	"github.com/gin-gonic/gin"
	"github.com/WnJee/gorig/apix"
	"github.com/WnJee/gorig/cache"
	"github.com/WnJee/gorig/global/consts"
)

func TimeRange(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	start, _ := apix.GetParamInt64(ctx, "start", apix.Force)
	end, _ := apix.GetParamInt64(ctx, "end", apix.Force)
	unit, _ := apix.GetParamStr(ctx, "unit", "minute")
	data, e := S().TimeRange(ctx, start, end, cache.Granularity(unit))
	apix.HandleData(ctx, consts.CurdSelectFailCode, data, e)
}
