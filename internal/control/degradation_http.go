package control

import (
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/storage/models"
)

const maxDegradationSearchRunes = 128

func (s *Server) handleDegradationOverview(c *gin.Context) {
	if !degradationQueryIsEmpty(c, "degradation_overview") {
		return
	}
	result, err := s.service.GetDegradationOverview(c.Request.Context())
	if err != nil {
		writeServiceError(c, "degradation_overview", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleUpdateDegradationSettings(c *gin.Context) {
	if !degradationQueryIsEmpty(c, "update_degradation_settings") {
		return
	}
	var request DegradationSettingsRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "update_degradation_settings", mapControlJSONError(err))
		return
	}
	result, err := s.service.UpdateDegradationSettings(c.Request.Context(), request)
	if err != nil {
		writeServiceError(c, "update_degradation_settings", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleListDegradationMonitors(c *gin.Context) {
	query, apiErr := parseDegradationMonitorQuery(c.Request.URL.RawQuery, c.Request.URL.ForceQuery)
	if apiErr != nil {
		writeServiceError(c, "list_degradation_monitors", apiErr)
		return
	}
	result, err := s.service.ListDegradationMonitors(c.Request.Context(), query)
	if err != nil {
		writeServiceError(c, "list_degradation_monitors", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleEnrollDegradationMonitors(c *gin.Context) {
	if !degradationQueryIsEmpty(c, "enroll_degradation_monitors") {
		return
	}
	var request DegradationEnrollRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "enroll_degradation_monitors", mapControlJSONError(err))
		return
	}
	result, err := s.service.EnrollDegradationMonitors(c.Request.Context(), request)
	if err != nil {
		writeServiceError(c, "enroll_degradation_monitors", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleUpdateDegradationMonitor(c *gin.Context) {
	id, ok := degradationMonitorID(c, "update_degradation_monitor")
	if !ok || !degradationQueryIsEmpty(c, "update_degradation_monitor") {
		return
	}
	var request DegradationMonitorUpdateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "update_degradation_monitor", mapControlJSONError(err))
		return
	}
	result, err := s.service.UpdateDegradationMonitor(c.Request.Context(), id, request)
	if err != nil {
		writeServiceError(c, "update_degradation_monitor", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleDeleteDegradationMonitor(c *gin.Context) {
	id, ok := degradationMonitorID(c, "delete_degradation_monitor")
	if !ok || !degradationQueryIsEmpty(c, "delete_degradation_monitor") {
		return
	}
	if err := s.service.DeleteDegradationMonitor(c.Request.Context(), id); err != nil {
		writeServiceError(c, "delete_degradation_monitor", err)
		return
	}
	response.SuccessI18n(c, "common.success", gin.H{"id": id})
}

func (s *Server) handleBatchDegradationMonitors(c *gin.Context) {
	if !degradationQueryIsEmpty(c, "batch_degradation_monitors") {
		return
	}
	var request DegradationBatchRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "batch_degradation_monitors", mapControlJSONError(err))
		return
	}
	result, err := s.service.BatchDegradationMonitors(c.Request.Context(), request)
	if err != nil {
		writeServiceError(c, "batch_degradation_monitors", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleRunDegradationMonitor(c *gin.Context) {
	id, ok := degradationMonitorID(c, "run_degradation_monitor")
	if !ok || !degradationQueryIsEmpty(c, "run_degradation_monitor") {
		return
	}
	if err := bindOptionalEmptyJSONObject(c); err != nil {
		writeServiceError(c, "run_degradation_monitor", mapControlJSONError(err))
		return
	}
	result, err := s.service.RunDegradationMonitor(
		c.Request.Context(), id, models.DegradationTriggerManual,
	)
	if err != nil {
		writeServiceError(c, "run_degradation_monitor", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleListDegradationRuns(c *gin.Context) {
	id, ok := degradationMonitorID(c, "list_degradation_runs")
	if !ok {
		return
	}
	limit, apiErr := parseDegradationRunLimit(c.Request.URL.RawQuery, c.Request.URL.ForceQuery)
	if apiErr != nil {
		writeServiceError(c, "list_degradation_runs", apiErr)
		return
	}
	result, err := s.service.ListDegradationRuns(c.Request.Context(), id, limit)
	if err != nil {
		writeServiceError(c, "list_degradation_runs", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func degradationMonitorID(c *gin.Context, operation string) (uint, bool) {
	parsed, err := parseCanonicalSafeUint(c.Param("monitor_id"))
	if err != nil || parsed == 0 {
		writeServiceError(c, operation, app_errors.ErrBadRequest)
		return 0, false
	}
	return uint(parsed), true
}

func degradationQueryIsEmpty(c *gin.Context, operation string) bool {
	if c.Request.URL.RawQuery == "" && !c.Request.URL.ForceQuery {
		return true
	}
	writeServiceError(c, operation, app_errors.ErrBadRequest)
	return false
}

func parseDegradationRunLimit(rawQuery string, forceQuery bool) (int, *app_errors.APIError) {
	if forceQuery && rawQuery == "" {
		return 0, app_errors.ErrBadRequest
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return 0, app_errors.ErrBadRequest
	}
	for key, entries := range values {
		if key != "limit" || len(entries) != 1 {
			return 0, app_errors.ErrBadRequest
		}
	}
	entries, exists := values["limit"]
	if !exists {
		return degradationRunHistoryLimit, nil
	}
	parsed, err := parseCanonicalSafeUint(entries[0])
	if err != nil || parsed == 0 || parsed > degradationRunHistoryLimit {
		return 0, app_errors.ErrBadRequest
	}
	return int(parsed), nil
}

func parseDegradationMonitorQuery(
	rawQuery string,
	forceQuery bool,
) (DegradationMonitorQuery, *app_errors.APIError) {
	query := DegradationMonitorQuery{
		Page: 1, PageSize: degradationDefaultPageSize,
	}
	if forceQuery && rawQuery == "" {
		return DegradationMonitorQuery{}, app_errors.ErrBadRequest
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return DegradationMonitorQuery{}, app_errors.ErrBadRequest
	}
	for key, entries := range values {
		switch key {
		case "query", "state", "group_id", "enabled", "page", "page_size":
		default:
			return DegradationMonitorQuery{}, app_errors.ErrBadRequest
		}
		if len(entries) != 1 {
			return DegradationMonitorQuery{}, app_errors.ErrBadRequest
		}
	}
	if entries, exists := values["query"]; exists {
		query.Query = strings.TrimSpace(entries[0])
		if utf8.RuneCountInString(query.Query) > maxDegradationSearchRunes {
			return DegradationMonitorQuery{}, app_errors.ErrBadRequest
		}
	}
	if entries, exists := values["state"]; exists {
		query.State = strings.TrimSpace(entries[0])
		if !models.DegradationState(query.State).Valid() {
			return DegradationMonitorQuery{}, app_errors.ErrBadRequest
		}
	}
	if entries, exists := values["group_id"]; exists {
		parsed, err := parseCanonicalSafeUint(entries[0])
		if err != nil || parsed == 0 {
			return DegradationMonitorQuery{}, app_errors.ErrBadRequest
		}
		query.GroupID = uint(parsed)
	}
	if entries, exists := values["enabled"]; exists {
		parsed, err := strconv.ParseBool(entries[0])
		if err != nil || (entries[0] != "true" && entries[0] != "false") {
			return DegradationMonitorQuery{}, app_errors.ErrBadRequest
		}
		query.Enabled = &parsed
	}
	if entries, exists := values["page"]; exists {
		parsed, err := parseCanonicalSafeUint(entries[0])
		if err != nil || parsed == 0 {
			return DegradationMonitorQuery{}, app_errors.ErrBadRequest
		}
		query.Page = int(parsed)
	}
	if entries, exists := values["page_size"]; exists {
		parsed, err := parseCanonicalSafeUint(entries[0])
		if err != nil || parsed == 0 || parsed > degradationMaxPageSize {
			return DegradationMonitorQuery{}, app_errors.ErrBadRequest
		}
		query.PageSize = int(parsed)
	}
	return query, nil
}

func degradationMonitorMutationLocator(c *gin.Context) string {
	return "degradation-monitor:" + mutationID(c.Param("monitor_id"))
}
