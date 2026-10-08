package controller

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestUpdateOptionValidatesInviteRewardRatio pins the server-side range of the
// invitation reward ratio. The option stores a decimal share (0.05 = 5%), so an
// out-of-range value — for example a client sending 5 meaning 500% — must be
// rejected instead of turning into a five-fold payout.
func TestUpdateOptionValidatesInviteRewardRatio(t *testing.T) {
	database := modelManagementDB(t, "sqlite", "")
	previousRatio := common.InviteRewardRatio
	t.Cleanup(func() { common.InviteRewardRatio = previousRatio })

	updateRatio := func(t *testing.T, value any) (bool, string) {
		t.Helper()
		var response struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		modelManagementRequest(t, UpdateOption, http.MethodPut, "/api/option/",
			OptionUpdateRequest{Key: "InviteRewardRatio", Value: value}, &response)
		return response.Success, response.Message
	}
	storedRatio := func(t *testing.T) (string, error) {
		t.Helper()
		var option model.Option
		err := database.Where("key = ?", "InviteRewardRatio").First(&option).Error
		return option.Value, err
	}

	for _, testCase := range []struct {
		name  string
		value any
	}{
		{"below zero", -0.01},
		{"above one", 1.01},
		{"percentage instead of share", 5},
		{"not a number", "NaN"},
	} {
		t.Run("rejects "+testCase.name, func(t *testing.T) {
			success, message := updateRatio(t, testCase.value)
			assert.False(t, success)
			assert.Contains(t, message, fmt.Sprint(testCase.value), "the error must name the rejected value")
			_, err := storedRatio(t)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound, "a rejected value must never reach the database")
		})
	}

	// Both boundaries are valid: 0 disables the payout, 1 pays 100%.
	success, _ := updateRatio(t, 0)
	require.True(t, success)
	stored, err := storedRatio(t)
	require.NoError(t, err)
	assert.Equal(t, "0", stored)
	assert.Zero(t, common.InviteRewardRatio)

	success, _ = updateRatio(t, 1)
	require.True(t, success)
	stored, err = storedRatio(t)
	require.NoError(t, err)
	assert.Equal(t, "1", stored)
	assert.Equal(t, 1.0, common.InviteRewardRatio)

	// A rejected update must leave the stored value untouched.
	success, _ = updateRatio(t, 1.5)
	assert.False(t, success)
	stored, err = storedRatio(t)
	require.NoError(t, err)
	assert.Equal(t, "1", stored)
}

// TestUpdateOptionAuditsInviteRewardRatioValue pins which option updates record
// their new value in the audit trail: only InviteRewardRatio, whose share is a
// money payout. Every other key keeps the historical "key name only" contract
// that keeps potentially sensitive values out of the audit table.
func TestUpdateOptionAuditsInviteRewardRatioValue(t *testing.T) {
	database := modelManagementDB(t, "sqlite", "")
	previousRatio := common.InviteRewardRatio
	previousSystemName := common.SystemName
	t.Cleanup(func() {
		common.InviteRewardRatio = previousRatio
		common.SystemName = previousSystemName
	})

	updateOption := func(t *testing.T, key string, value any) {
		t.Helper()
		var response struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		modelManagementRequest(t, UpdateOption, http.MethodPut, "/api/option/",
			OptionUpdateRequest{Key: key, Value: value}, &response)
		require.True(t, response.Success, response.Message)
	}
	latestAuditParams := func(t *testing.T) map[string]any {
		t.Helper()
		var entry model.AuditLog
		require.NoError(t, database.Where("action = ?", "option.update").Order("id desc").First(&entry).Error)
		require.NotNil(t, entry.Other.Op)
		require.Equal(t, "option.update", entry.Other.Op.Action)
		encoded, err := common.Marshal(entry.Other.Op.Params)
		require.NoError(t, err)
		var params map[string]any
		require.NoError(t, common.Unmarshal(encoded, &params))
		return params
	}

	updateOption(t, "InviteRewardRatio", 0.05)
	params := latestAuditParams(t)
	assert.Equal(t, "InviteRewardRatio", params["key"])
	assert.Equal(t, "0.05", params["value"], "the audit must answer what the ratio was changed to")

	// Every other key stays minimal: the key name only, never the value.
	updateOption(t, "SystemName", "audited-site")
	params = latestAuditParams(t)
	assert.Equal(t, "SystemName", params["key"])
	assert.NotContains(t, params, "value")
}
