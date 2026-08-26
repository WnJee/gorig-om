package memstat

import (
	"github.com/gin-gonic/gin"
	"github.com/jom-io/gorig/apix"
	"github.com/jom-io/gorig/global/consts"
)

func BigTop(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	start, _ := apix.GetParamInt64(ctx, "start", apix.Force)
	end, _ := apix.GetParamInt64(ctx, "end", apix.Force)
	page, _ := apix.GetParamInt64(ctx, "page", apix.NotForce, 0)
	size, _ := apix.GetParamInt64(ctx, "size", apix.NotForce, 0)
	limit, _ := apix.GetParamInt64(ctx, "limit", apix.NotForce, 0)
	sortBy, _ := apix.GetParamStr(ctx, "sortBy", "inuseSpace")
	asc, _ := apix.GetParamBool(ctx, "asc", false)
	if size <= 0 && limit > 0 {
		size = limit
	}
	data, e := S().BigTop(ctx, start, end, page, size, sortBy, asc)
	apix.HandleData(ctx, consts.CurdSelectFailCode, data, e)
}

func BigCount(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	start, _ := apix.GetParamInt64(ctx, "start", apix.Force)
	end, _ := apix.GetParamInt64(ctx, "end", apix.Force)
	data, e := S().BigCount(ctx, start, end)
	apix.HandleData(ctx, consts.CurdSelectFailCode, data, e)
}

func LeakLatest(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	data, e := S().LeakLatest(ctx)
	if data != nil {
		data.BaseProfile = ""
		data.LeakProfile = ""
	}
	apix.HandleData(ctx, consts.CurdSelectFailCode, data, e)
}

func LeakCount(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	start, _ := apix.GetParamInt64(ctx, "start", apix.Force)
	end, _ := apix.GetParamInt64(ctx, "end", apix.Force)
	data, e := S().LeakCount(ctx, start, end)
	apix.HandleData(ctx, consts.CurdSelectFailCode, data, e)
}

func LeakPage(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	start, _ := apix.GetParamInt64(ctx, "start", apix.NotForce, 0)
	end, _ := apix.GetParamInt64(ctx, "end", apix.NotForce, 0)
	page, _ := apix.GetParamInt64(ctx, "page", apix.NotForce, 1)
	size, _ := apix.GetParamInt64(ctx, "size", apix.NotForce, 10)
	data, e := S().LeakPage(ctx, start, end, page, size)
	if data != nil {
		for _, item := range data.Items {
			if item == nil {
				continue
			}
			item.BaseProfile = ""
			item.LeakProfile = ""
		}
	}
	apix.HandleData(ctx, consts.CurdSelectFailCode, data, e)
}
