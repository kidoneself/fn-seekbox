package cloud

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

func remoteXunleiEnabled() bool { return os.Getenv("SEEKBOX_REMOTE_XUNLEI") != "0" }

type remoteQRState struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	AccountID     string `json:"account_id"`
	Status        string `json:"status"`
	Image         string `json:"image"`
	QRURL         string `json:"qr_url"`
	ExpiresAt     int64  `json:"expires_at"`
	QueuePosition int    `json:"queue_position"`
}

func (s *Service) remoteQRCall(ctx context.Context, method, route, token string, body, out any) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = s.centerRequest(ctx, method, route, token, body, out)
		if err == nil || ctx.Err() != nil {
			return err
		}
		var failure *centerError
		delay := time.Duration(attempt+1) * time.Second
		if errors.As(err, &failure) {
			if failure.Status < 500 && failure.Status != 429 {
				return err
			}
			if failure.RetryAfter > delay {
				delay = min(failure.RetryAfter, 10*time.Second)
			}
		}
		if attempt == 2 {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func (s *Service) runRemoteQR(ctx context.Context, q *qrLogin) {
	fail := func(message string) {
		q.mu.Lock()
		defer q.mu.Unlock()
		if ctx.Err() == nil && q.state.Status != "connected" && q.state.Status != "cancelled" {
			q.state.Status = "failed"
			q.state.Message = message
			q.credentials = nil
			q.state.Image = ""
			q.state.QRURL = ""
		}
	}
	// Reuse the installation identity without enabling anonymous reporting when it
	// was disabled. The lock prevents registration racing with the report worker.
	s.centerMu.Lock()
	state, err := s.store.read(q.owner)
	var secret Credentials
	if err == nil {
		secret, err = s.centerRegister(ctx, q.owner, state.Center)
	}
	s.centerMu.Unlock()
	if err != nil {
		fail("无法连接扫码中心，请稍后重试")
		return
	}
	token := secret["report_token"]
	accountRef, requestID, claimID := newID(), q.state.ID, newID()
	body := map[string]string{"provider": "xunlei", "account_id": accountRef, "request_id": requestID}
	var remote remoteQRState
	if err = s.remoteQRCall(ctx, "POST", "/api/client/scan/sessions", token, body, &remote); err != nil {
		fail("扫码服务暂时不可用或正在限流，请稍后重试")
		return
	}
	if remote.ID == "" || len(remote.ID) != 36 || strings.ContainsAny(remote.ID, "/\\?#") || remote.Provider != "xunlei" || remote.AccountID != accountRef {
		fail("扫码中心响应异常，请稍后重试")
		return
	}
	route := "/api/client/scan/sessions/" + url.PathEscape(remote.ID)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Also handles lost create/claim/ack replies; cancellation never logs out a
		// cloud account and only clears this temporary broker session.
		_ = s.centerRequest(cleanup, "DELETE", route, token, map[string]any{}, nil)
	}()
	deadline, _ := ctx.Deadline()
	for {
		if ctx.Err() != nil {
			return
		}
		if remote.Provider != "xunlei" || remote.AccountID != accountRef {
			fail("扫码会话信息不匹配，请重新获取")
			return
		}
		q.mu.Lock()
		if remote.ExpiresAt > now() {
			q.state.ExpiresAt = min(remote.ExpiresAt, deadline.UnixMilli())
		}
		switch remote.Status {
		case "queued":
			q.state.Status = "queued"
			q.state.Message = fmt.Sprintf("扫码服务繁忙，前方还有 %d 个会话", max(0, remote.QueuePosition-1))
		case "starting":
			q.state.Status = "starting"
			q.state.Message = "正在获取迅雷登录二维码"
		case "waiting":
			q.state.Status = "waiting"
			q.state.Message = "请用迅雷 App 扫码，并在手机上确认登录"
		case "scanned":
			q.state.Status = "scanned"
			q.state.Message = "已扫码，请在手机上确认登录"
		case "verifying":
			q.state.Status = "verifying"
			q.state.Message = "正在验证迅雷登录凭据"
		case "failed", "expired", "cancelled":
			q.state.Status = remote.Status
			q.state.Message = "扫码未完成或已过期，请重新获取"
			q.state.Image = ""
			q.mu.Unlock()
			return
		case "ready":
		default:
			q.mu.Unlock()
			fail("扫码中心返回了未知状态，请重新获取")
			return
		}
		if strings.HasPrefix(remote.Image, "data:image/png;base64,") && len(remote.Image) < 800000 {
			q.state.Image = remote.Image
		}
		q.mu.Unlock()
		if remote.Status == "ready" {
			claim := map[string]string{"account_id": accountRef, "claim_id": claimID}
			var result struct {
				Provider    string      `json:"provider"`
				AccountID   string      `json:"account_id"`
				Credentials Credentials `json:"credentials"`
			}
			if err = s.remoteQRCall(ctx, "POST", route+"/claim", token, claim, &result); err != nil {
				fail("领取登录凭据失败，请重新扫码")
				return
			}
			session, sessionErr := xunleiSession(result.Credentials)
			if result.Provider != "xunlei" || result.AccountID != accountRef || sessionErr != nil || session == nil {
				fail("迅雷登录凭据不完整，请重新扫码")
				return
			}
			q.mu.Lock()
			if ctx.Err() != nil {
				q.mu.Unlock()
				return
			}
			q.credentials = result.Credentials
			q.state.Status = "ready"
			q.state.Message = "手机已确认，正在校验并保存账号"
			q.state.Image = ""
			q.state.QRURL = ""
			q.remoteAck = func(ackCtx context.Context) error {
				return s.remoteQRCall(ackCtx, "POST", route+"/ack", token, claim, nil)
			}
			q.mu.Unlock()
			<-ctx.Done()
			return
		}
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err = s.remoteQRCall(ctx, "GET", route, token, nil, &remote); err != nil {
			fail("扫码连接中断，请重新获取二维码")
			return
		}
	}
}
