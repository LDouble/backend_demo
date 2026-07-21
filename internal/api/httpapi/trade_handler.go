package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	tradedomain "github.com/weouc-plus/campus-platform/internal/modules/trade/domain"
)

func (h *Handler) listMyTradeOrders(c *gin.Context) {
	page, size := paging(c)
	rows, total, err := h.trades.List(c.Request.Context(), c.GetUint64(userIDKey), page, size)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, pageData(tradeOrderViews(rows), page, size, total))
}

func (h *Handler) getMyTradeOrder(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	order, err := h.trades.Get(c.Request.Context(), c.GetUint64(userIDKey), id)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, tradeOrderViewOf(order))
}

func (h *Handler) cancelTradeOrder(c *gin.Context)   { h.finishTradeOrder(c, false) }
func (h *Handler) completeTradeOrder(c *gin.Context) { h.finishTradeOrder(c, true) }

func (h *Handler) finishTradeOrder(c *gin.Context, complete bool) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req marketplaceVersionRequest
	if !bind(c, &req) {
		return
	}
	order, err := h.trades.Get(c.Request.Context(), c.GetUint64(userIDKey), id)
	if err != nil {
		failure(c, err)
		return
	}
	if order.OrderType != tradedomain.OrderTypeMarketplace {
		failure(c, apperror.New(409, "unsupported_order_type", "该订单类型尚未接入取消或完成操作"))
		return
	}
	if complete {
		order, err = h.marketplace.Complete(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion)
	} else {
		order, err = h.marketplace.Cancel(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion)
	}
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, tradeOrderViewOf(order))
}

type tradeOrderView struct {
	ID                uint64          `json:"id"`
	OrderNo           string          `json:"order_no"`
	OrderType         string          `json:"order_type"`
	ResourceType      string          `json:"resource_type"`
	ResourceID        uint64          `json:"resource_id"`
	BuyerID           uint64          `json:"buyer_id"`
	SellerID          uint64          `json:"seller_id"`
	AmountCents       int64           `json:"amount_cents"`
	Currency          string          `json:"currency"`
	PaymentMode       string          `json:"payment_mode"`
	TradeStatus       string          `json:"trade_status"`
	FulfillmentStatus string          `json:"fulfillment_status"`
	TitleSnapshot     string          `json:"title_snapshot"`
	ResourceSnapshot  json.RawMessage `json:"resource_snapshot"`
	ExpiresAt         *time.Time      `json:"expires_at"`
	CompletedAt       *time.Time      `json:"completed_at"`
	CancelledAt       *time.Time      `json:"cancelled_at"`
	Version           uint64          `json:"version"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

func tradeOrderViewOf(order *tradedomain.Order) tradeOrderView {
	return tradeOrderView{ID: order.ID, OrderNo: order.OrderNo, OrderType: order.OrderType, ResourceType: order.ResourceType, ResourceID: order.ResourceId, BuyerID: order.BuyerId, SellerID: order.SellerId, AmountCents: order.AmountCents, Currency: order.Currency, PaymentMode: order.PaymentMode, TradeStatus: order.TradeStatus, FulfillmentStatus: order.FulfillmentStatus, TitleSnapshot: order.TitleSnapshot, ResourceSnapshot: json.RawMessage(order.ResourceSnapshot), ExpiresAt: order.ExpiresAt, CompletedAt: order.CompletedAt, CancelledAt: order.CancelledAt, Version: order.Version, CreatedAt: order.CreatedAt, UpdatedAt: order.UpdatedAt}
}

func tradeOrderViews(orders []tradedomain.Order) []tradeOrderView {
	views := make([]tradeOrderView, len(orders))
	for i := range orders {
		views[i] = tradeOrderViewOf(&orders[i])
	}
	return views
}
