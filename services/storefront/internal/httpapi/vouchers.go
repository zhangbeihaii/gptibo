package httpapi

import (
	"github.com/gin-gonic/gin"
	"gptibo/storefront/internal/auth"
	"gptibo/storefront/internal/fault"
	"gptibo/storefront/internal/vouchers"
)

func registerVouchers(admin *gin.RouterGroup, service *vouchers.Service) {
	admin.GET("/vouchers", func(c *gin.Context) {
		if service == nil {
			fail(c, fault.New(503, "卡密库存服务暂不可用。"))
			return
		}
		out, err := service.Summary(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, out)
	})
	admin.POST("/vouchers/import", func(c *gin.Context) {
		var in struct {
			PlanID string `json:"planId"`
			Codes  string `json:"codes"`
		}
		if !readLimit(c, &in, 65536) {
			return
		}
		principal := c.MustGet("principal").(auth.Principal)
		out, err := service.Import(c.Request.Context(), principal.Subject, in.PlanID, []byte(in.Codes))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(201, out)
	})
}
