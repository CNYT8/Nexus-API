/*
Copyright (C) 2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ticket_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ticketCreateRequest struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type ticketReplyRequest struct {
	Content string `json:"content"`
}

type ticketStatusRequest struct {
	Status string `json:"status"`
}

const ticketRequestBodyLimit = 256 << 10

func decodeTicketJSON(c *gin.Context, value any) error {
	if c.Request.Body == nil {
		return errors.New("ticket request body is required")
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, ticketRequestBodyLimit)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("ticket request must contain one JSON value")
		}
		return err
	}
	return nil
}

func ticketFeatureEnabled(c *gin.Context) bool {
	if ticket_setting.IsEnabled() {
		return true
	}
	common.ApiErrorI18n(c, i18n.MsgTicketDisabled)
	return false
}

func ticketAdminManageEnabled(c *gin.Context) bool {
	if ticket_setting.IsAdminManageEnabled() || c.GetInt("role") == common.RoleRootUser {
		return true
	}
	common.ApiErrorI18n(c, i18n.MsgTicketAdminManageDisabled)
	return false
}

func parseTicketId(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgTicketNotFound)
		return 0, false
	}
	return id, true
}

func ticketError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorI18n(c, i18n.MsgTicketNotFound)
	case errors.Is(err, model.ErrTicketContentRequired):
		common.ApiErrorI18n(c, i18n.MsgTicketContentRequired)
	case errors.Is(err, model.ErrTicketContentTooLong):
		common.ApiErrorI18n(c, i18n.MsgTicketContentTooLong)
	case errors.Is(err, model.ErrTicketInvalidType):
		common.ApiErrorI18n(c, i18n.MsgTicketInvalidType)
	case errors.Is(err, model.ErrTicketInvalidStatus):
		common.ApiErrorI18n(c, i18n.MsgTicketInvalidStatus)
	case errors.Is(err, model.ErrTicketInvalidAuthor):
		common.ApiErrorI18n(c, i18n.MsgTicketInvalidPayload)
	case errors.Is(err, model.ErrTicketClosed):
		common.ApiErrorI18n(c, i18n.MsgTicketClosed)
	case errors.Is(err, model.ErrTicketOpenLimit):
		common.ApiErrorI18n(c, i18n.MsgTicketOpenLimit)
	case errors.Is(err, model.ErrTicketMessageLimit):
		common.ApiErrorI18n(c, i18n.MsgTicketMessageLimit)
	default:
		common.SysLog("ticket operation failed: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
	}
}

func GetTicketSettings(c *gin.Context) {
	common.ApiSuccess(c, ticket_setting.GetSettings())
}

func getTicketPageQuery(c *gin.Context) *common.PageInfo {
	pageInfo := common.GetPageQuery(c)
	if pageInfo.Page < 1 {
		pageInfo.Page = 1
	}
	if pageInfo.PageSize < 1 {
		pageInfo.PageSize = common.ItemsPerPage
	}
	return pageInfo
}

func GetMyTickets(c *gin.Context) {
	if !ticketFeatureEnabled(c) {
		return
	}
	pageInfo := getTicketPageQuery(c)
	tickets, total, err := model.ListTicketsByUser(c.GetInt("id"), pageInfo)
	if err != nil {
		ticketError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(tickets)
	common.ApiSuccess(c, pageInfo)
}

func CreateMyTicket(c *gin.Context) {
	if !ticketFeatureEnabled(c) {
		return
	}
	var request ticketCreateRequest
	if err := decodeTicketJSON(c, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgTicketInvalidPayload)
		return
	}
	ticket, err := model.CreateTicket(c.GetInt("id"), request.Type, request.Content)
	if err != nil {
		ticketError(c, err)
		return
	}
	common.ApiSuccess(c, ticket)
}

func GetMyTicket(c *gin.Context) {
	if !ticketFeatureEnabled(c) {
		return
	}
	ticketId, ok := parseTicketId(c)
	if !ok {
		return
	}
	ticket, err := model.GetTicketView(ticketId, c.GetInt("id"), false)
	if err != nil {
		ticketError(c, err)
		return
	}
	common.ApiSuccess(c, ticket)
}

func ReplyMyTicket(c *gin.Context) {
	if !ticketFeatureEnabled(c) {
		return
	}
	ticketId, ok := parseTicketId(c)
	if !ok {
		return
	}
	var request ticketReplyRequest
	if err := decodeTicketJSON(c, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgTicketInvalidPayload)
		return
	}
	ticket, err := model.AddTicketReply(ticketId, c.GetInt("id"), model.TicketAuthorUser, request.Content)
	if err != nil {
		ticketError(c, err)
		return
	}
	common.ApiSuccess(c, ticket)
}

func GetAdminTickets(c *gin.Context) {
	if !ticketFeatureEnabled(c) || !ticketAdminManageEnabled(c) {
		return
	}
	pageInfo := getTicketPageQuery(c)
	tickets, total, err := model.ListTicketsForAdmin(c.Query("status"), pageInfo)
	if err != nil {
		ticketError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(tickets)
	common.ApiSuccess(c, pageInfo)
}

func GetAdminTicket(c *gin.Context) {
	if !ticketFeatureEnabled(c) || !ticketAdminManageEnabled(c) {
		return
	}
	ticketId, ok := parseTicketId(c)
	if !ok {
		return
	}
	ticket, err := model.GetTicketView(ticketId, 0, true)
	if err != nil {
		ticketError(c, err)
		return
	}
	common.ApiSuccess(c, ticket)
}

func ReplyAdminTicket(c *gin.Context) {
	if !ticketFeatureEnabled(c) || !ticketAdminManageEnabled(c) {
		return
	}
	ticketId, ok := parseTicketId(c)
	if !ok {
		return
	}
	var request ticketReplyRequest
	if err := decodeTicketJSON(c, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgTicketInvalidPayload)
		return
	}
	ticket, err := model.AddTicketReply(ticketId, c.GetInt("id"), model.TicketAuthorAdmin, request.Content)
	if err != nil {
		ticketError(c, err)
		return
	}
	common.ApiSuccess(c, ticket)
}

func UpdateAdminTicketStatus(c *gin.Context) {
	if !ticketFeatureEnabled(c) || !ticketAdminManageEnabled(c) {
		return
	}
	ticketId, ok := parseTicketId(c)
	if !ok {
		return
	}
	var request ticketStatusRequest
	if err := decodeTicketJSON(c, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgTicketInvalidPayload)
		return
	}
	if request.Status != model.TicketStatusClosed && request.Status != model.TicketStatusPending {
		common.ApiErrorI18n(c, i18n.MsgTicketInvalidStatus)
		return
	}
	if !ticket_setting.GetSettings().AdminCanClose && c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorI18n(c, i18n.MsgTicketAdminCloseDisabled)
		return
	}
	ticket, err := model.SetTicketStatus(ticketId, request.Status)
	if err != nil {
		ticketError(c, err)
		return
	}
	common.ApiSuccess(c, ticket)
}

func UpdateTicketSettings(c *gin.Context) {
	var settings ticket_setting.Settings
	if err := decodeTicketJSON(c, &settings); err != nil {
		common.ApiErrorI18n(c, i18n.MsgTicketInvalidSettings)
		return
	}
	if settings.MaxContentLength < ticket_setting.MinMaxContentLength || settings.MaxContentLength > ticket_setting.MaxMaxContentLength {
		common.ApiErrorI18n(c, i18n.MsgTicketContentLengthInvalid)
		return
	}
	values := map[string]string{
		"ticket_setting.enabled":              strconv.FormatBool(settings.Enabled),
		"ticket_setting.admin_manage_enabled": strconv.FormatBool(settings.AdminManageEnabled),
		"ticket_setting.admin_can_close":      strconv.FormatBool(settings.AdminCanClose),
		"ticket_setting.max_content_length":   strconv.Itoa(settings.MaxContentLength),
	}
	if err := model.UpdateOptionsBulk(values); err != nil {
		common.SysLog("failed to update ticket settings: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": common.TranslateMessage(c, i18n.MsgDatabaseError),
		})
		return
	}
	common.ApiSuccess(c, ticket_setting.GetSettings())
}
