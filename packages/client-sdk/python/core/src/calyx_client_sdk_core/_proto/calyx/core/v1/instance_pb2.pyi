from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class InstanceStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    INSTANCE_STATUS_UNSPECIFIED: _ClassVar[InstanceStatus]
    INSTANCE_STATUS_CONNECTED: _ClassVar[InstanceStatus]
    INSTANCE_STATUS_CONNECTING: _ClassVar[InstanceStatus]
    INSTANCE_STATUS_NEEDS_AUTH: _ClassVar[InstanceStatus]
    INSTANCE_STATUS_DISCONNECTED: _ClassVar[InstanceStatus]
    INSTANCE_STATUS_ERROR: _ClassVar[InstanceStatus]

class Provider(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    PROVIDER_UNSPECIFIED: _ClassVar[Provider]
    PROVIDER_WHATSAPP_WEB: _ClassVar[Provider]
    PROVIDER_META_CLOUD: _ClassVar[Provider]

class DeviceIdentity(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    DEVICE_IDENTITY_UNSPECIFIED: _ClassVar[DeviceIdentity]
    DEVICE_IDENTITY_CHROME_WINDOWS: _ClassVar[DeviceIdentity]
    DEVICE_IDENTITY_EDGE_WINDOWS: _ClassVar[DeviceIdentity]
    DEVICE_IDENTITY_FIREFOX_WINDOWS: _ClassVar[DeviceIdentity]
    DEVICE_IDENTITY_FIREFOX_LINUX: _ClassVar[DeviceIdentity]
    DEVICE_IDENTITY_SAFARI_MAC_OS: _ClassVar[DeviceIdentity]
INSTANCE_STATUS_UNSPECIFIED: InstanceStatus
INSTANCE_STATUS_CONNECTED: InstanceStatus
INSTANCE_STATUS_CONNECTING: InstanceStatus
INSTANCE_STATUS_NEEDS_AUTH: InstanceStatus
INSTANCE_STATUS_DISCONNECTED: InstanceStatus
INSTANCE_STATUS_ERROR: InstanceStatus
PROVIDER_UNSPECIFIED: Provider
PROVIDER_WHATSAPP_WEB: Provider
PROVIDER_META_CLOUD: Provider
DEVICE_IDENTITY_UNSPECIFIED: DeviceIdentity
DEVICE_IDENTITY_CHROME_WINDOWS: DeviceIdentity
DEVICE_IDENTITY_EDGE_WINDOWS: DeviceIdentity
DEVICE_IDENTITY_FIREFOX_WINDOWS: DeviceIdentity
DEVICE_IDENTITY_FIREFOX_LINUX: DeviceIdentity
DEVICE_IDENTITY_SAFARI_MAC_OS: DeviceIdentity

class WebhookConfig(_message.Message):
    __slots__ = ("url", "event", "secret")
    URL_FIELD_NUMBER: _ClassVar[int]
    EVENT_FIELD_NUMBER: _ClassVar[int]
    SECRET_FIELD_NUMBER: _ClassVar[int]
    url: str
    event: str
    secret: str
    def __init__(self, url: _Optional[str] = ..., event: _Optional[str] = ..., secret: _Optional[str] = ...) -> None: ...

class PairingCodeSettings(_message.Message):
    __slots__ = ("phone_number",)
    PHONE_NUMBER_FIELD_NUMBER: _ClassVar[int]
    phone_number: str
    def __init__(self, phone_number: _Optional[str] = ...) -> None: ...

class WhatsAppWebSettings(_message.Message):
    __slots__ = ("pairing_qr", "pairing_code", "device_identity", "locale")
    PAIRING_QR_FIELD_NUMBER: _ClassVar[int]
    PAIRING_CODE_FIELD_NUMBER: _ClassVar[int]
    DEVICE_IDENTITY_FIELD_NUMBER: _ClassVar[int]
    LOCALE_FIELD_NUMBER: _ClassVar[int]
    pairing_qr: bool
    pairing_code: PairingCodeSettings
    device_identity: DeviceIdentity
    locale: str
    def __init__(self, pairing_qr: bool = ..., pairing_code: _Optional[_Union[PairingCodeSettings, _Mapping]] = ..., device_identity: _Optional[_Union[DeviceIdentity, str]] = ..., locale: _Optional[str] = ...) -> None: ...

class MetaCloudSettings(_message.Message):
    __slots__ = ("phone_number_id", "access_token", "app_secret")
    PHONE_NUMBER_ID_FIELD_NUMBER: _ClassVar[int]
    ACCESS_TOKEN_FIELD_NUMBER: _ClassVar[int]
    APP_SECRET_FIELD_NUMBER: _ClassVar[int]
    phone_number_id: str
    access_token: str
    app_secret: str
    def __init__(self, phone_number_id: _Optional[str] = ..., access_token: _Optional[str] = ..., app_secret: _Optional[str] = ...) -> None: ...

class CreateInstanceRequest(_message.Message):
    __slots__ = ("instance_name", "is_used_only_for_send_messages", "webhooks", "whatsapp_web", "meta_cloud")
    INSTANCE_NAME_FIELD_NUMBER: _ClassVar[int]
    IS_USED_ONLY_FOR_SEND_MESSAGES_FIELD_NUMBER: _ClassVar[int]
    WEBHOOKS_FIELD_NUMBER: _ClassVar[int]
    WHATSAPP_WEB_FIELD_NUMBER: _ClassVar[int]
    META_CLOUD_FIELD_NUMBER: _ClassVar[int]
    instance_name: str
    is_used_only_for_send_messages: bool
    webhooks: _containers.RepeatedCompositeFieldContainer[WebhookConfig]
    whatsapp_web: WhatsAppWebSettings
    meta_cloud: MetaCloudSettings
    def __init__(self, instance_name: _Optional[str] = ..., is_used_only_for_send_messages: bool = ..., webhooks: _Optional[_Iterable[_Union[WebhookConfig, _Mapping]]] = ..., whatsapp_web: _Optional[_Union[WhatsAppWebSettings, _Mapping]] = ..., meta_cloud: _Optional[_Union[MetaCloudSettings, _Mapping]] = ...) -> None: ...

class CreateInstanceResponse(_message.Message):
    __slots__ = ("status", "pairing_code", "message", "device_identity", "locale")
    STATUS_FIELD_NUMBER: _ClassVar[int]
    PAIRING_CODE_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    DEVICE_IDENTITY_FIELD_NUMBER: _ClassVar[int]
    LOCALE_FIELD_NUMBER: _ClassVar[int]
    status: InstanceStatus
    pairing_code: str
    message: str
    device_identity: DeviceIdentity
    locale: str
    def __init__(self, status: _Optional[_Union[InstanceStatus, str]] = ..., pairing_code: _Optional[str] = ..., message: _Optional[str] = ..., device_identity: _Optional[_Union[DeviceIdentity, str]] = ..., locale: _Optional[str] = ...) -> None: ...

class RemoveInstanceRequest(_message.Message):
    __slots__ = ("instance_name",)
    INSTANCE_NAME_FIELD_NUMBER: _ClassVar[int]
    instance_name: str
    def __init__(self, instance_name: _Optional[str] = ...) -> None: ...

class RemoveInstanceResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class WebhookList(_message.Message):
    __slots__ = ("webhooks",)
    WEBHOOKS_FIELD_NUMBER: _ClassVar[int]
    webhooks: _containers.RepeatedCompositeFieldContainer[WebhookConfig]
    def __init__(self, webhooks: _Optional[_Iterable[_Union[WebhookConfig, _Mapping]]] = ...) -> None: ...

class UpdateInstanceConfigRequest(_message.Message):
    __slots__ = ("instance_name", "new_instance_name", "is_enabled", "is_used_only_for_send_messages", "webhooks", "meta_cloud")
    INSTANCE_NAME_FIELD_NUMBER: _ClassVar[int]
    NEW_INSTANCE_NAME_FIELD_NUMBER: _ClassVar[int]
    IS_ENABLED_FIELD_NUMBER: _ClassVar[int]
    IS_USED_ONLY_FOR_SEND_MESSAGES_FIELD_NUMBER: _ClassVar[int]
    WEBHOOKS_FIELD_NUMBER: _ClassVar[int]
    META_CLOUD_FIELD_NUMBER: _ClassVar[int]
    instance_name: str
    new_instance_name: str
    is_enabled: bool
    is_used_only_for_send_messages: bool
    webhooks: WebhookList
    meta_cloud: MetaCloudSettings
    def __init__(self, instance_name: _Optional[str] = ..., new_instance_name: _Optional[str] = ..., is_enabled: bool = ..., is_used_only_for_send_messages: bool = ..., webhooks: _Optional[_Union[WebhookList, _Mapping]] = ..., meta_cloud: _Optional[_Union[MetaCloudSettings, _Mapping]] = ...) -> None: ...

class UpdateInstanceConfigResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListInstancesRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListInstancesResponse(_message.Message):
    __slots__ = ("instances",)
    INSTANCES_FIELD_NUMBER: _ClassVar[int]
    instances: _containers.RepeatedCompositeFieldContainer[InstanceInfo]
    def __init__(self, instances: _Optional[_Iterable[_Union[InstanceInfo, _Mapping]]] = ...) -> None: ...

class InstanceInfo(_message.Message):
    __slots__ = ("instance_name", "provider", "connection_status", "is_enabled", "is_used_only_for_send_messages", "webhooks", "device_identity", "locale")
    INSTANCE_NAME_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_FIELD_NUMBER: _ClassVar[int]
    CONNECTION_STATUS_FIELD_NUMBER: _ClassVar[int]
    IS_ENABLED_FIELD_NUMBER: _ClassVar[int]
    IS_USED_ONLY_FOR_SEND_MESSAGES_FIELD_NUMBER: _ClassVar[int]
    WEBHOOKS_FIELD_NUMBER: _ClassVar[int]
    DEVICE_IDENTITY_FIELD_NUMBER: _ClassVar[int]
    LOCALE_FIELD_NUMBER: _ClassVar[int]
    instance_name: str
    provider: Provider
    connection_status: InstanceStatus
    is_enabled: bool
    is_used_only_for_send_messages: bool
    webhooks: _containers.RepeatedCompositeFieldContainer[WebhookSettings]
    device_identity: DeviceIdentity
    locale: str
    def __init__(self, instance_name: _Optional[str] = ..., provider: _Optional[_Union[Provider, str]] = ..., connection_status: _Optional[_Union[InstanceStatus, str]] = ..., is_enabled: bool = ..., is_used_only_for_send_messages: bool = ..., webhooks: _Optional[_Iterable[_Union[WebhookSettings, _Mapping]]] = ..., device_identity: _Optional[_Union[DeviceIdentity, str]] = ..., locale: _Optional[str] = ...) -> None: ...

class WebhookSettings(_message.Message):
    __slots__ = ("url", "event", "is_active", "has_secret")
    URL_FIELD_NUMBER: _ClassVar[int]
    EVENT_FIELD_NUMBER: _ClassVar[int]
    IS_ACTIVE_FIELD_NUMBER: _ClassVar[int]
    HAS_SECRET_FIELD_NUMBER: _ClassVar[int]
    url: str
    event: str
    is_active: bool
    has_secret: bool
    def __init__(self, url: _Optional[str] = ..., event: _Optional[str] = ..., is_active: bool = ..., has_secret: bool = ...) -> None: ...

class GetInstanceStatusRequest(_message.Message):
    __slots__ = ("instance_name",)
    INSTANCE_NAME_FIELD_NUMBER: _ClassVar[int]
    instance_name: str
    def __init__(self, instance_name: _Optional[str] = ...) -> None: ...

class GetInstanceStatusResponse(_message.Message):
    __slots__ = ("status",)
    STATUS_FIELD_NUMBER: _ClassVar[int]
    status: InstanceStatus
    def __init__(self, status: _Optional[_Union[InstanceStatus, str]] = ...) -> None: ...
