import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class MessageDeliveryStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    MESSAGE_DELIVERY_STATUS_UNSPECIFIED: _ClassVar[MessageDeliveryStatus]
    MESSAGE_DELIVERY_STATUS_SENT: _ClassVar[MessageDeliveryStatus]
    MESSAGE_DELIVERY_STATUS_DELIVERED: _ClassVar[MessageDeliveryStatus]
    MESSAGE_DELIVERY_STATUS_READ: _ClassVar[MessageDeliveryStatus]
    MESSAGE_DELIVERY_STATUS_FAILED: _ClassVar[MessageDeliveryStatus]
MESSAGE_DELIVERY_STATUS_UNSPECIFIED: MessageDeliveryStatus
MESSAGE_DELIVERY_STATUS_SENT: MessageDeliveryStatus
MESSAGE_DELIVERY_STATUS_DELIVERED: MessageDeliveryStatus
MESSAGE_DELIVERY_STATUS_READ: MessageDeliveryStatus
MESSAGE_DELIVERY_STATUS_FAILED: MessageDeliveryStatus

class SubscribeRequest(_message.Message):
    __slots__ = ("instance_names", "event_types")
    INSTANCE_NAMES_FIELD_NUMBER: _ClassVar[int]
    EVENT_TYPES_FIELD_NUMBER: _ClassVar[int]
    instance_names: _containers.RepeatedScalarFieldContainer[str]
    event_types: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, instance_names: _Optional[_Iterable[str]] = ..., event_types: _Optional[_Iterable[str]] = ...) -> None: ...

class Event(_message.Message):
    __slots__ = ("id", "instance_name", "type", "payload_json", "occurred_at")
    ID_FIELD_NUMBER: _ClassVar[int]
    INSTANCE_NAME_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    PAYLOAD_JSON_FIELD_NUMBER: _ClassVar[int]
    OCCURRED_AT_FIELD_NUMBER: _ClassVar[int]
    id: str
    instance_name: str
    type: str
    payload_json: str
    occurred_at: _timestamp_pb2.Timestamp
    def __init__(self, id: _Optional[str] = ..., instance_name: _Optional[str] = ..., type: _Optional[str] = ..., payload_json: _Optional[str] = ..., occurred_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class ConnectionUpdate(_message.Message):
    __slots__ = ("status",)
    STATUS_FIELD_NUMBER: _ClassVar[int]
    status: str
    def __init__(self, status: _Optional[str] = ...) -> None: ...

class MessageReceived(_message.Message):
    __slots__ = ("message_id", "chat_id", "chat_alt_id", "from_alt_id", "is_group", "push_name", "timestamp", "type", "text")
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    CHAT_ALT_ID_FIELD_NUMBER: _ClassVar[int]
    FROM_FIELD_NUMBER: _ClassVar[int]
    FROM_ALT_ID_FIELD_NUMBER: _ClassVar[int]
    IS_GROUP_FIELD_NUMBER: _ClassVar[int]
    PUSH_NAME_FIELD_NUMBER: _ClassVar[int]
    TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    chat_id: str
    chat_alt_id: str
    from_alt_id: str
    is_group: bool
    push_name: str
    timestamp: int
    type: str
    text: str
    def __init__(self, message_id: _Optional[str] = ..., chat_id: _Optional[str] = ..., chat_alt_id: _Optional[str] = ..., from_alt_id: _Optional[str] = ..., is_group: bool = ..., push_name: _Optional[str] = ..., timestamp: _Optional[int] = ..., type: _Optional[str] = ..., text: _Optional[str] = ..., **kwargs) -> None: ...

class MessageStatus(_message.Message):
    __slots__ = ("message_id", "chat_id", "chat_alt_id", "status")
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    CHAT_ALT_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    chat_id: str
    chat_alt_id: str
    status: str
    def __init__(self, message_id: _Optional[str] = ..., chat_id: _Optional[str] = ..., chat_alt_id: _Optional[str] = ..., status: _Optional[str] = ...) -> None: ...

class BulkFinished(_message.Message):
    __slots__ = ("job_id", "status", "total_messages", "sent_messages", "failed_messages", "error")
    JOB_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_MESSAGES_FIELD_NUMBER: _ClassVar[int]
    SENT_MESSAGES_FIELD_NUMBER: _ClassVar[int]
    FAILED_MESSAGES_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    job_id: str
    status: str
    total_messages: int
    sent_messages: int
    failed_messages: int
    error: str
    def __init__(self, job_id: _Optional[str] = ..., status: _Optional[str] = ..., total_messages: _Optional[int] = ..., sent_messages: _Optional[int] = ..., failed_messages: _Optional[int] = ..., error: _Optional[str] = ...) -> None: ...
