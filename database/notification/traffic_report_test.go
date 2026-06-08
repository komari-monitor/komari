package notification

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/komari-monitor/komari/database/models"
)

func TestValidateTrafficReportNotificationsRejectsEnabledWithoutCadence(t *testing.T) {
	err := ValidateTrafficReportNotifications([]models.TrafficReportNotification{{
		Client: "client-a",
		Enable: true,
	}})

	assert.Error(t, err)
}

func TestBuildEnabledTrafficReportNotificationsRequiresExistingCadence(t *testing.T) {
	_, err := buildEnabledTrafficReportNotifications([]string{"client-a"}, nil)
	assert.Error(t, err)

	_, err = buildEnabledTrafficReportNotifications([]string{"client-a"}, []models.TrafficReportNotification{{Client: "client-a"}})
	assert.Error(t, err)

	notifications, err := buildEnabledTrafficReportNotifications([]string{"client-a"}, []models.TrafficReportNotification{{
		Client: "client-a",
		Daily:  true,
	}})
	require.NoError(t, err)
	require.Len(t, notifications, 1)
	assert.Equal(t, "client-a", notifications[0].Client)
	assert.True(t, notifications[0].Enable)
}
