package core

import (
	"context"
	"errors"
	"io"

	"calyx/backend/internal/apierr"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const (
	methodListInstances = "/calyx.core.v1.InstanceService/ListInstances"
	methodGetStatus     = "/calyx.core.v1.InstanceService/GetInstanceStatus"
	methodUpdate        = "/calyx.core.v1.InstanceService/UpdateInstanceConfig"
	methodRemove        = "/calyx.core.v1.InstanceService/RemoveInstance"
	methodCreate        = "/calyx.core.v1.InstanceService/CreateInstance"
)

// ListInstances returns every instance of the core. An empty answer is an empty, non-nil slice.
func (c *Client) ListInstances(ctx context.Context) ([]InstanceInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	resp, err := c.instances.ListInstances(ctx, &corev1.ListInstancesRequest{})
	if err != nil {
		return nil, mapError(callRead, methodListInstances, err)
	}
	out := make([]InstanceInfo, 0, len(resp.GetInstances()))
	for _, in := range resp.GetInstances() {
		out = append(out, InstanceInfo{
			CoreName:       in.GetInstanceName(),
			Provider:       providerName(in.GetProvider()),
			Status:         statusName(in.GetConnectionStatus()),
			Enabled:        in.GetIsEnabled(),
			SendOnly:       in.GetIsUsedOnlyForSendMessages(),
			DeviceIdentity: identityName(in.GetDeviceIdentity()),
			Locale:         in.GetLocale(),
		})
	}
	return out, nil
}

func (c *Client) GetInstanceStatus(ctx context.Context, coreName string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	resp, err := c.instances.GetInstanceStatus(ctx, &corev1.GetInstanceStatusRequest{InstanceName: coreName})
	if err != nil {
		return "", mapError(callRead, methodGetStatus, err)
	}
	return statusName(resp.GetStatus()), nil
}

// UpdateInstanceConfig sends the full configuration of a change. On any error the caller saves nothing.
func (c *Client) UpdateInstanceConfig(ctx context.Context, cfg InstanceConfig) error {
	ctx, cancel := context.WithTimeout(ctx, changeTimeout)
	defer cancel()
	_, err := c.instances.UpdateInstanceConfig(ctx, updateRequest(cfg))
	return mapError(callChange, methodUpdate, err)
}

// RemoveInstance removes an instance. NOT_FOUND counts as done.
func (c *Client) RemoveInstance(ctx context.Context, coreName string) error {
	ctx, cancel := context.WithTimeout(ctx, removeTimeout)
	defer cancel()
	_, err := c.instances.RemoveInstance(ctx, &corev1.RemoveInstanceRequest{InstanceName: coreName})
	return mapError(callRemove, methodRemove, err)
}

// ErrEndedWithoutConnected is wrapped by the error of CreateInstance when the stream ends without
// a CONNECTED update and without an error. Callers test it with errors.Is.
var ErrEndedWithoutConnected = errors.New("the core ended the pairing without CONNECTED")

// Update is one answer of the pairing stream. PairingValue is the core's pairing_code field
// (QR text or an 8-character code); the caller decides which one it is. DeviceIdentity and Locale
// are set only on the CONNECTED update of a WhatsApp Web instance.
type Update struct {
	Status, PairingValue, Message string
	DeviceIdentity, Locale        string
}

// CreateInstance creates or re-pairs an instance and reads the pairing stream. It adds no deadline:
// ctx carries the pairing timeout. It calls onUpdate once per answer, in order, on the caller's
// goroutine. It returns nil only when the last update was CONNECTED and the stream ended.
func (c *Client) CreateInstance(ctx context.Context, cfg InstanceConfig, onUpdate func(Update)) error {
	req, err := createRequest(cfg)
	if err != nil {
		return err
	}
	stream, err := c.instances.CreateInstance(ctx, req)
	if err != nil {
		return createError(err)
	}
	connected := false
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			if connected {
				return nil
			}
			return apierr.Internal(ErrEndedWithoutConnected)
		}
		if err != nil {
			return createError(err)
		}
		u := Update{
			Status: statusName(resp.GetStatus()), PairingValue: resp.GetPairingCode(), Message: resp.GetMessage(),
			DeviceIdentity: identityName(resp.GetDeviceIdentity()), Locale: resp.GetLocale(),
		}
		onUpdate(u)
		connected = u.Status == StatusConnected
	}
}
