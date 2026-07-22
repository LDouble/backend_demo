package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/api/generated"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
)

func (h *Handler) getAcademicVerification(c *gin.Context) {
	status, err := h.academic.Status(c.Request.Context(), c.GetUint64(userIDKey))
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, status)
}

func (h *Handler) uploadAcademicVerificationMaterial(c *gin.Context) {
	header, err := c.FormFile("file")
	if err != nil {
		failure(c, apperror.New(http.StatusBadRequest, "missing_material", "缺少学生证图片"))
		return
	}
	file, err := header.Open()
	if err != nil {
		failure(c, apperror.New(http.StatusBadRequest, "invalid_material", "无法读取学生证图片"))
		return
	}
	defer func() { _ = file.Close() }()
	material, err := h.academic.Upload(c.Request.Context(), c.GetUint64(userIDKey), file)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusCreated, gin.H{
		"material_id": material.ID,
		"mime_type":   material.MimeType,
		"size_bytes":  material.SizeBytes,
		"expires_at":  material.ExpiresAt,
	})
}

func (h *Handler) submitStudentCardVerification(c *gin.Context) {
	var request generated.SubmitStudentCardVerificationJSONRequestBody
	if !bind(c, &request) {
		return
	}
	row, err := h.academic.SubmitStudentCard(
		c.Request.Context(),
		c.GetUint64(userIDKey),
		request.RealName,
		request.StudentNo,
		request.MaterialId,
	)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusCreated, row)
}

func (h *Handler) verifyAcademicCredentials(c *gin.Context) {
	var request generated.VerifyAcademicCredentialsJSONRequestBody
	if !bind(c, &request) {
		return
	}
	row, err := h.academic.VerifyCredentials(
		c.Request.Context(),
		c.GetUint64(userIDKey),
		request.StudentNo,
		request.Password,
		c.ClientIP(),
	)
	request.Password = ""
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, row)
}

func (h *Handler) listAdminAcademicVerificationRequests(c *gin.Context) {
	params, _ := generatedParams[generated.ListAdminAcademicVerificationRequestsParams](
		c,
		"ListAdminAcademicVerificationRequests",
	)
	status := ""
	if params.Status != nil {
		status = *params.Status
	}
	page, pageSize := paging(c)
	rows, total, err := h.academic.ListRequests(c.Request.Context(), status, page, pageSize)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, pageData(rows, page, pageSize, total))
}

func (h *Handler) getAdminAcademicVerificationRequest(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	row, err := h.academic.GetRequest(c.Request.Context(), id)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, row)
}

func (h *Handler) getAdminAcademicVerificationMaterial(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	content, err := h.academic.OpenMaterial(c.Request.Context(), id)
	if err != nil {
		failure(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", "inline")
	c.Data(http.StatusOK, content.MIMEType, content.Data)
}

func (h *Handler) approveAcademicVerificationRequest(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request generated.ApproveAcademicVerificationRequestJSONRequestBody
	if !bind(c, &request) {
		return
	}
	row, err := h.academic.Approve(
		c.Request.Context(), id, c.GetUint64(userIDKey), request.ExpectedVersion,
	)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, row)
}

func (h *Handler) rejectAcademicVerificationRequest(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request generated.RejectAcademicVerificationRequestJSONRequestBody
	if !bind(c, &request) {
		return
	}
	row, err := h.academic.Reject(
		c.Request.Context(), id, c.GetUint64(userIDKey), request.ExpectedVersion, request.Reason,
	)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, row)
}

func (h *Handler) revokeAcademicIdentity(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request generated.RevokeAcademicIdentityJSONRequestBody
	if !bind(c, &request) {
		return
	}
	identity, err := h.academic.Revoke(
		c.Request.Context(), id, c.GetUint64(userIDKey), request.ExpectedVersion, request.Reason,
	)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, identity)
}
