package grpcserver

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

type instanceService struct {
	corev1.UnimplementedInstanceServiceServer
	o *engine.Orchestrator
}

var pairingPhone = regexp.MustCompile(`^\d{7,15}$`)

func (s *instanceService) CreateInstance(req *corev1.CreateInstanceRequest, stream grpc.ServerStreamingServer[corev1.CreateInstanceResponse]) error {
	params := engine.CreateInstanceParams{
		Name:     req.GetInstanceName(),
		SendOnly: req.IsUsedOnlyForSendMessages,
		Webhooks: webhookInputs(req.GetWebhooks()),
	}

	switch settings := req.GetProviderSettings().(type) {
	case nil:
		// Defaults to WhatsApp Web with QR pairing.
		params.Provider, params.Pairing = "WHATSAPP_WEB", engine.PairingMethod{QR: true}
	case *corev1.CreateInstanceRequest_WhatsappWeb:
		params.Provider = "WHATSAPP_WEB"
		pairing, err := pairingMethod(settings.WhatsappWeb)
		if err != nil {
			return err
		}
		params.Pairing = pairing
		params.DeviceIdentity = settings.WhatsappWeb.GetDeviceIdentity()
		params.Locale = settings.WhatsappWeb.GetLocale()
	case *corev1.CreateInstanceRequest_MetaCloud:
		params.Provider = "META_CLOUD"
		config, err := metaConfig(settings.MetaCloud)
		if err != nil {
			return err
		}
		params.ProviderConfig = config
	}

	return s.o.CreateInstance(stream.Context(), params, func(update engine.CreateInstanceUpdate) {
		resp := &corev1.CreateInstanceResponse{Status: update.Status, Message: update.Message}
		if update.PairingCode != "" {
			resp.PairingCode = &update.PairingCode
		}
		resp.DeviceIdentity = update.DeviceIdentity
		if update.Locale != "" {
			resp.Locale = &update.Locale
		}
		// A failed Send means the client left; the cancelled context stops the pairing.
		_ = stream.Send(resp)
	})
}

func pairingMethod(settings *corev1.WhatsAppWebSettings) (engine.PairingMethod, error) {
	switch method := settings.GetPairingMethod().(type) {
	case nil:
		return engine.PairingMethod{QR: true}, nil
	case *corev1.WhatsAppWebSettings_PairingQr:
		if !method.PairingQr {
			return engine.PairingMethod{}, calyxerr.New(codes.InvalidArgument,
				"pairing_qr must be true. To pair with a code, set pairing_code instead.")
		}
		return engine.PairingMethod{QR: true}, nil
	default:
		phone := settings.GetPairingCode().GetPhoneNumber()
		if !pairingPhone.MatchString(phone) {
			return engine.PairingMethod{}, calyxerr.New(codes.InvalidArgument,
				"pairing_code.phone_number must be 7 to 15 digits with the country code, for example 34600111222.")
		}
		return engine.PairingMethod{PhoneNumber: phone}, nil
	}
}

func metaConfig(settings *corev1.MetaCloudSettings) (string, error) {
	credentials := engine.MetaCredentials{
		PhoneNumberID: strings.TrimSpace(settings.GetPhoneNumberId()),
		AccessToken:   strings.TrimSpace(settings.GetAccessToken()),
		AppSecret:     strings.TrimSpace(settings.GetAppSecret()),
	}
	if credentials.PhoneNumberID == "" || credentials.AccessToken == "" || credentials.AppSecret == "" {
		return "", calyxerr.New(codes.InvalidArgument, "meta_cloud needs phone_number_id, access_token and app_secret.")
	}
	config, err := json.Marshal(credentials)
	return string(config), err
}

func webhookInputs(webhooks []*corev1.WebhookConfig) []engine.WebhookInput {
	inputs := make([]engine.WebhookInput, 0, len(webhooks))
	for _, w := range webhooks {
		inputs = append(inputs, engine.WebhookInput{URL: w.GetUrl(), Event: w.GetEvent(), Secret: w.GetSecret()})
	}
	return inputs
}

func (s *instanceService) RemoveInstance(ctx context.Context, req *corev1.RemoveInstanceRequest) (*corev1.RemoveInstanceResponse, error) {
	if err := s.o.RemoveInstance(ctx, req.GetInstanceName()); err != nil {
		return nil, err
	}
	return &corev1.RemoveInstanceResponse{}, nil
}

func (s *instanceService) UpdateInstanceConfig(ctx context.Context, req *corev1.UpdateInstanceConfigRequest) (*corev1.UpdateInstanceConfigResponse, error) {
	changes := engine.UpdateInstanceParams{
		NewName:   req.NewInstanceName,
		IsEnabled: req.IsEnabled,
		SendOnly:  req.IsUsedOnlyForSendMessages,
	}
	if req.Webhooks != nil {
		webhooks := webhookInputs(req.Webhooks.GetWebhooks())
		changes.Webhooks = &webhooks
	}
	if req.MetaCloud != nil {
		config, err := metaConfig(req.MetaCloud)
		if err != nil {
			return nil, err
		}
		changes.ProviderConfig = &config
	}
	if err := s.o.UpdateInstanceConfig(ctx, req.GetInstanceName(), changes); err != nil {
		return nil, err
	}
	return &corev1.UpdateInstanceConfigResponse{}, nil
}

func (s *instanceService) ListInstances(ctx context.Context, req *corev1.ListInstancesRequest) (*corev1.ListInstancesResponse, error) {
	summaries, err := s.o.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	resp := &corev1.ListInstancesResponse{}
	for _, summary := range summaries {
		info := &corev1.InstanceInfo{
			InstanceName:              summary.Name,
			Provider:                  providerEnum(summary.Provider),
			ConnectionStatus:          summary.Status,
			IsEnabled:                 summary.IsEnabled,
			IsUsedOnlyForSendMessages: summary.SendOnly,
			DeviceIdentity:            summary.DeviceIdentity,
		}
		if summary.Locale != "" {
			info.Locale = &summary.Locale
		}
		for _, w := range summary.Webhooks {
			info.Webhooks = append(info.Webhooks, &corev1.WebhookSettings{
				Url: w.URL, Event: w.Event, IsActive: w.IsActive, HasSecret: w.HasSecret,
			})
		}
		resp.Instances = append(resp.Instances, info)
	}
	return resp, nil
}

func providerEnum(provider string) corev1.Provider {
	return corev1.Provider(corev1.Provider_value["PROVIDER_"+provider])
}

func (s *instanceService) GetInstanceStatus(ctx context.Context, req *corev1.GetInstanceStatusRequest) (*corev1.GetInstanceStatusResponse, error) {
	instanceStatus, err := s.o.GetInstanceStatus(ctx, req.GetInstanceName())
	if err != nil {
		return nil, err
	}
	return &corev1.GetInstanceStatusResponse{Status: instanceStatus}, nil
}
