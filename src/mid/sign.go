package mid

import (
	"github.com/gin-gonic/gin"
	"github.com/jom-io/gorig-om/src/omuser"
	"github.com/jom-io/gorig/apix/response"
	"github.com/jom-io/gorig/mid/tokenx"
	"strings"
)

func Sign() gin.HandlerFunc {
	return func(c *gin.Context) {
		sign := c.GetHeader("Authorization")
		if sign == "" || !strings.HasPrefix(sign, "Bearer ") {
			if c.Query("token") == "" {
				response.ErrorForbidden(c)
				return
			}
			sign = c.Query("token")
		}
		sign = strings.TrimPrefix(sign, "Bearer ")
		get := tokenx.Get(tokenx.Jwt, tokenx.Memory)
		if _, err := get.Generator.Parse(sign); err != nil {
			response.ErrorForbidden(c)
			return
		}
		userID, exist := get.Manager.GetUserID(sign)
		if !exist {
			response.ErrorTokenAuthFail(c)
			return
		}
		if !omuser.IsOM(userID) {
			response.ErrorForbidden(c)
			return
		}
		c.Next()
	}
}
