import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ErrorReason(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ERROR_REASON_UNSPECIFIED: _ClassVar[ErrorReason]
    ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT: _ClassVar[ErrorReason]
    ERROR_REASON_SEND_IN_PROGRESS: _ClassVar[ErrorReason]
    ERROR_REASON_SEND_OUTCOME_UNKNOWN: _ClassVar[ErrorReason]
    ERROR_REASON_DUPLICATE_MESSAGE: _ClassVar[ErrorReason]
    ERROR_REASON_INSTANCE_QUEUE_FULL: _ClassVar[ErrorReason]

class BulkJobStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    BULK_JOB_STATUS_UNSPECIFIED: _ClassVar[BulkJobStatus]
    BULK_JOB_STATUS_QUEUED: _ClassVar[BulkJobStatus]
    BULK_JOB_STATUS_RUNNING: _ClassVar[BulkJobStatus]
    BULK_JOB_STATUS_COMPLETED: _ClassVar[BulkJobStatus]
    BULK_JOB_STATUS_CANCELLED: _ClassVar[BulkJobStatus]
    BULK_JOB_STATUS_FAILED: _ClassVar[BulkJobStatus]
ERROR_REASON_UNSPECIFIED: ErrorReason
ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT: ErrorReason
ERROR_REASON_SEND_IN_PROGRESS: ErrorReason
ERROR_REASON_SEND_OUTCOME_UNKNOWN: ErrorReason
ERROR_REASON_DUPLICATE_MESSAGE: ErrorReason
ERROR_REASON_INSTANCE_QUEUE_FULL: ErrorReason
BULK_JOB_STATUS_UNSPECIFIED: BulkJobStatus
BULK_JOB_STATUS_QUEUED: BulkJobStatus
BULK_JOB_STATUS_RUNNING: BulkJobStatus
BULK_JOB_STATUS_COMPLETED: BulkJobStatus
BULK_JOB_STATUS_CANCELLED: BulkJobStatus
BULK_JOB_STATUS_FAILED: BulkJobStatus

class SendPlainTextRequest(_message.Message):
    __slots__ = ("instance_name", "to", "text", "idempotency_key")
    INSTANCE_NAME_FIELD_NUMBER: _ClassVar[int]
    TO_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    instance_name: str
    to: str
    text: str
    idempotency_key: str
    def __init__(self, instance_name: _Optional[str] = ..., to: _Optional[str] = ..., text: _Optional[str] = ..., idempotency_key: _Optional[str] = ...) -> None: ...

class SendPlainTextResponse(_message.Message):
    __slots__ = ("message_id", "replayed")
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    REPLAYED_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    replayed: bool
    def __init__(self, message_id: _Optional[str] = ..., replayed: bool = ...) -> None: ...

class IntRange(_message.Message):
    __slots__ = ("min", "max")
    MIN_FIELD_NUMBER: _ClassVar[int]
    MAX_FIELD_NUMBER: _ClassVar[int]
    min: int
    max: int
    def __init__(self, min: _Optional[int] = ..., max: _Optional[int] = ...) -> None: ...

class BulkPacing(_message.Message):
    __slots__ = ("delay", "pause_every", "pause_duration")
    DELAY_FIELD_NUMBER: _ClassVar[int]
    PAUSE_EVERY_FIELD_NUMBER: _ClassVar[int]
    PAUSE_DURATION_FIELD_NUMBER: _ClassVar[int]
    delay: IntRange
    pause_every: IntRange
    pause_duration: IntRange
    def __init__(self, delay: _Optional[_Union[IntRange, _Mapping]] = ..., pause_every: _Optional[_Union[IntRange, _Mapping]] = ..., pause_duration: _Optional[_Union[IntRange, _Mapping]] = ...) -> None: ...

class SendBulkSameMessageRequest(_message.Message):
    __slots__ = ("instance_name", "recipients", "text", "pacing", "idempotency_key")
    INSTANCE_NAME_FIELD_NUMBER: _ClassVar[int]
    RECIPIENTS_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    PACING_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    instance_name: str
    recipients: _containers.RepeatedScalarFieldContainer[str]
    text: str
    pacing: BulkPacing
    idempotency_key: str
    def __init__(self, instance_name: _Optional[str] = ..., recipients: _Optional[_Iterable[str]] = ..., text: _Optional[str] = ..., pacing: _Optional[_Union[BulkPacing, _Mapping]] = ..., idempotency_key: _Optional[str] = ...) -> None: ...

class PlainTextPayload(_message.Message):
    __slots__ = ("to", "text", "delay_ms")
    TO_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    DELAY_MS_FIELD_NUMBER: _ClassVar[int]
    to: str
    text: str
    delay_ms: int
    def __init__(self, to: _Optional[str] = ..., text: _Optional[str] = ..., delay_ms: _Optional[int] = ...) -> None: ...

class SendBulkCustomMessagesRequest(_message.Message):
    __slots__ = ("instance_name", "messages", "pacing", "idempotency_key")
    INSTANCE_NAME_FIELD_NUMBER: _ClassVar[int]
    MESSAGES_FIELD_NUMBER: _ClassVar[int]
    PACING_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    instance_name: str
    messages: _containers.RepeatedCompositeFieldContainer[PlainTextPayload]
    pacing: BulkPacing
    idempotency_key: str
    def __init__(self, instance_name: _Optional[str] = ..., messages: _Optional[_Iterable[_Union[PlainTextPayload, _Mapping]]] = ..., pacing: _Optional[_Union[BulkPacing, _Mapping]] = ..., idempotency_key: _Optional[str] = ...) -> None: ...

class BulkJob(_message.Message):
    __slots__ = ("id", "instance_name", "status", "total_messages", "sent_messages", "failed_messages", "progress_percent", "estimated_minutes", "display_time", "created_at", "started_at", "estimated_finish_at", "finished_at", "error", "replayed")
    ID_FIELD_NUMBER: _ClassVar[int]
    INSTANCE_NAME_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_MESSAGES_FIELD_NUMBER: _ClassVar[int]
    SENT_MESSAGES_FIELD_NUMBER: _ClassVar[int]
    FAILED_MESSAGES_FIELD_NUMBER: _ClassVar[int]
    PROGRESS_PERCENT_FIELD_NUMBER: _ClassVar[int]
    ESTIMATED_MINUTES_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_TIME_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    STARTED_AT_FIELD_NUMBER: _ClassVar[int]
    ESTIMATED_FINISH_AT_FIELD_NUMBER: _ClassVar[int]
    FINISHED_AT_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    REPLAYED_FIELD_NUMBER: _ClassVar[int]
    id: str
    instance_name: str
    status: BulkJobStatus
    total_messages: int
    sent_messages: int
    failed_messages: int
    progress_percent: float
    estimated_minutes: float
    display_time: str
    created_at: _timestamp_pb2.Timestamp
    started_at: _timestamp_pb2.Timestamp
    estimated_finish_at: _timestamp_pb2.Timestamp
    finished_at: _timestamp_pb2.Timestamp
    error: str
    replayed: bool
    def __init__(self, id: _Optional[str] = ..., instance_name: _Optional[str] = ..., status: _Optional[_Union[BulkJobStatus, str]] = ..., total_messages: _Optional[int] = ..., sent_messages: _Optional[int] = ..., failed_messages: _Optional[int] = ..., progress_percent: _Optional[float] = ..., estimated_minutes: _Optional[float] = ..., display_time: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., started_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., estimated_finish_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., finished_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., error: _Optional[str] = ..., replayed: bool = ...) -> None: ...

class GetBulkJobRequest(_message.Message):
    __slots__ = ("job_id",)
    JOB_ID_FIELD_NUMBER: _ClassVar[int]
    job_id: str
    def __init__(self, job_id: _Optional[str] = ...) -> None: ...

class GetBulkJobsRequest(_message.Message):
    __slots__ = ("job_ids",)
    JOB_IDS_FIELD_NUMBER: _ClassVar[int]
    job_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, job_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class GetBulkJobsResponse(_message.Message):
    __slots__ = ("jobs",)
    JOBS_FIELD_NUMBER: _ClassVar[int]
    jobs: _containers.RepeatedCompositeFieldContainer[BulkJob]
    def __init__(self, jobs: _Optional[_Iterable[_Union[BulkJob, _Mapping]]] = ...) -> None: ...

class CancelBulkJobRequest(_message.Message):
    __slots__ = ("job_id",)
    JOB_ID_FIELD_NUMBER: _ClassVar[int]
    job_id: str
    def __init__(self, job_id: _Optional[str] = ...) -> None: ...
