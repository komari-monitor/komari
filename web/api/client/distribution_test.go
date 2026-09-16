package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	v2 "github.com/komari-monitor/komari/protocol/v2"
)

func TestUploadV2RPCRejectsOtherAgentDistributions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		header string
	}{
		{name: "missing"},
		{name: "official", header: "komari-monitor/komari-agent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/api/clients/v2/rpc", strings.NewReader(`{}`))
			if tt.header != "" {
				context.Request.Header.Set(v2.DistributionHeader, tt.header)
			}

			UploadV2RPC(context)

			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
			}
			if got := recorder.Header().Get(v2.DistributionHeader); got != "" {
				t.Fatalf("server distribution header = %q on rejected request", got)
			}
		})
	}
}

func TestUploadV2RPCIdentifiesSIXOSNServer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/clients/v2/rpc", strings.NewReader(`{}`))
	context.Request.Header.Set(v2.DistributionHeader, v2.AgentDistribution)

	UploadV2RPC(context)

	if got := recorder.Header().Get(v2.DistributionHeader); got != v2.ServerDistribution {
		t.Fatalf("server distribution header = %q, want %q", got, v2.ServerDistribution)
	}
}
