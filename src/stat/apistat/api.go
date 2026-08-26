package apistat

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
	unit, _ := apix.GetParamStr(ctx, "unit", "hour")
	filter, _ := apix.GetParamArray[ApiStatType](ctx, "filter", apix.NotForce)

	data, e := S().TimeRange(ctx, start, end, cache.Granularity(unit), filter...)
	apix.HandleData(ctx, consts.CurdSelectFailCode, data, e)
}

func Summary(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	start, _ := apix.GetParamInt64(ctx, "start", apix.Force)
	end, _ := apix.GetParamInt64(ctx, "end", apix.Force)
	slowMs, _ := apix.GetParamInt64(ctx, "slowMs", apix.NotForce, 200)

	data, e := S().Summary(ctx, start, end, slowMs)
	apix.HandleData(ctx, consts.CurdSelectFailCode, data, e)
}

func Top(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	start, _ := apix.GetParamInt64(ctx, "start", apix.Force)
	end, _ := apix.GetParamInt64(ctx, "end", apix.Force)
	pageReq, _ := apix.GetPageReq(ctx)
	methods, _ := apix.GetParamArray[string](ctx, "methods", apix.NotForce)
	negMethods, _ := apix.GetParamArray[string](ctx, "negMethods", apix.NotForce)
	uriPrefix, _ := apix.GetParamStr(ctx, "uriPrefix")
	uriLike, _ := apix.GetParamStr(ctx, "uriLike")
	statuses, _ := apix.GetParamArray[string](ctx, "statuses", apix.NotForce)
	sortBy, _ := apix.GetParamStr(ctx, "sortBy", "avg")
	asc, _ := apix.GetParamBool(ctx, "asc", false)

	data, e := S().TopPage(ctx, start, end, pageReq.Page, pageReq.Size, methods, negMethods, uriPrefix, uriLike, statuses, sortBy, asc)
	apix.HandleData(ctx, consts.CurdSelectFailCode, data, e)
}

func Sample(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	method, _ := apix.GetParamForce(ctx, "method")
	uri, _ := apix.GetParamForce(ctx, "uri")
	types, _ := apix.GetParamArray[string](ctx, "types", apix.NotForce)

	data, e := S().Sample(ctx, method, uri, types)
	apix.HandleData(ctx, consts.CurdSelectFailCode, data, e)
}
