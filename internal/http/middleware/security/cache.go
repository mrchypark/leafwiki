package security

import "github.com/gin-gonic/gin"

// NoStore prevents visibility-filtered responses from surviving publication changes.
func NoStore() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}
