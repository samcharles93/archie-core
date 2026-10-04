package archiemessaging

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

type messagingControlPlane struct{ client pb.ControlPlaneServiceClient }

func (c messagingControlPlane) Catalog(ctx context.Context) ([]messaging.SettingDescriptor, error) {
	response, err := c.client.Catalog(ctx, &pb.CatalogRequest{})
	if err != nil {
		return nil, settingsRPCError(err)
	}
	descriptors := make([]messaging.SettingDescriptor, 0, len(response.Resources))
	for _, resource := range response.Resources {
		var schema struct {
			Properties map[string]struct {
				Type      string `json:"type"`
				Format    string `json:"format"`
				WriteOnly bool   `json:"writeOnly"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(resource.SchemaJson), &schema); err != nil {
			return nil, errors.Join(messaging.ErrSettingsValidation, err)
		}
		fields := make(map[string]messaging.SettingField, len(schema.Properties))
		for name, property := range schema.Properties {
			fieldType := property.Type
			if property.Format == "duration" || strings.HasSuffix(name, "_seconds") {
				fieldType = "duration"
			}
			fields[name] = messaging.SettingField{Type: fieldType, Secret: property.WriteOnly || secretFieldName(name)}
		}
		descriptors = append(descriptors, messaging.SettingDescriptor{Kind: resource.Kind, Title: resource.Title, Fields: fields})
	}
	return descriptors, nil
}

func (c messagingControlPlane) Query(ctx context.Context, kind string) (messaging.SettingResource, error) {
	response, err := c.client.Query(ctx, &pb.QueryRequest{Kind: kind})
	if err != nil {
		return messaging.SettingResource{}, settingsRPCError(err)
	}
	return settingResource(response.Resource)
}

func (c messagingControlPlane) Command(ctx context.Context, command messaging.SettingCommand) (messaging.SettingResource, error) {
	input := any(command.Value)
	if command.RawValue != nil {
		input = command.RawValue
	}
	value, err := json.Marshal(input)
	if err != nil {
		return messaging.SettingResource{}, errors.Join(messaging.ErrSettingsValidation, err)
	}
	response, err := c.client.Command(ctx, &pb.CommandRequest{Kind: command.Kind, Command: "replace", ValueJson: value, ExpectedVersion: command.ExpectedVersion, RequestId: command.RequestID, Actor: command.Actor, Source: command.Source})
	if err != nil {
		return messaging.SettingResource{}, settingsRPCError(err)
	}
	return settingResource(response.Resource)
}

func settingResource(resource *pb.Resource) (messaging.SettingResource, error) {
	if resource == nil {
		return messaging.SettingResource{}, messaging.ErrSettingsUnavailable
	}
	var raw any
	if err := json.Unmarshal(resource.ValueJson, &raw); err != nil {
		return messaging.SettingResource{}, errors.Join(messaging.ErrSettingsValidation, err)
	}
	value, _ := raw.(map[string]any)
	return messaging.SettingResource{Kind: resource.Kind, Version: resource.Version, Value: value, Raw: raw}, nil
}

func settingsRPCError(err error) error {
	switch status.Code(err) {
	case codes.InvalidArgument:
		return errors.Join(messaging.ErrSettingsValidation, err)
	case codes.NotFound:
		return errors.Join(messaging.ErrSettingsNotFound, err)
	case codes.Aborted:
		return errors.Join(messaging.ErrSettingsConflict, err)
	default:
		return errors.Join(messaging.ErrSettingsUnavailable, err)
	}
}

func secretFieldName(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "secret") || strings.Contains(name, "password") || strings.Contains(name, "token") || strings.Contains(name, "credential") || strings.Contains(name, "key")
}
