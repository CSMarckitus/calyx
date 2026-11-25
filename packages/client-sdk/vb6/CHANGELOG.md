# Changelog of the Calyx client SDK for VB6

Each version lists its changes. The version of the SDK is `CALYX_SDK_VERSION` in
`src/CalyxCore.bas`, and every file of `src/` has the same version in its first comment line.

## Unreleased

`pacing_limit` is now `instance_queue_full`. `RetryAfter` is set for `send_in_progress` and
`instance_queue_full`. The retry example waits `RetryAfter` seconds.

## 0.1.0 (2026-11-21)

The first version. Not published. The author ran the SDK on Windows on 2026-11-25, and the
checks pass.

Added:

- `CalyxClient` with 7 backend operations (8 methods: `createBulkJob` has `CreateBulkJobSameText`
  and `CreateBulkJobCustom`): `SendMessage`, `CreateBulkJobSameText`,
  `CreateBulkJobCustom`, `GetBulkJob`, `ListBulkJobs`, `CancelBulkJob`, `ListInstances` and
  `GetInstance`. Each call is asynchronous, with the events `Completed` and `Failed`.
- `Resend`, which sends the request of a failure again with the same idempotency key. The SDK
  never resends by itself.
- `AbortAll`, which stops the wait for every pending call and raises `Failed` with `aborted`.
- The blocking methods `SendMessageSync`, `GetInstanceSync` and `ResendSync`, for programs with
  no window.
- The saved answers of `GetBulkJob` and `ListBulkJobs` (25 seconds).
- `CalyxError` with the retry advice of `retry-advice.json`, `CanResend` and `WinHttpError`.
- `CalyxSendResult`, `CalyxBulkMessage`, `CalyxFieldError`, `CalyxNewIdempotencyKey` and
  `CalyxField`.
- `CalyxJson.bas`, a copy of VBA-JSON v2.3.1 (MIT) with the prefix `Calyx` on each public name.
- The examples `FirstMessage`, `RetryWithResend`, `ScheduledSend` and `BulkJob`.

Requires the backend of this repository on 2026-11-21 or later.
