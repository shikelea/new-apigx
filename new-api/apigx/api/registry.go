package apigxapi

import (
	"net/http"

	"github.com/QuantumNous/new-api/apigx/modelfp"
	"github.com/gin-gonic/gin"
)

func (h *handler) getModelRegistry(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": modelfp.Registry()})
}
