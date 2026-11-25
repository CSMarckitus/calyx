import logging
import os
import sys
import time

from calyx_client_sdk import (
    BulkFinishedEvent,
    CalyxError,
    ClientCode,
    ConnectionUpdateEvent,
    InvalidPayloadEvent,
    MessageReceivedEvent,
    MessageStatusEvent,
    OtherEvent,
    RetryAdvice,
)

from calyx_client_sdk_core import CoreClient

log = logging.getLogger(__name__)


def follow(client: CoreClient) -> None:
    """Handle the events until the stream breaks.

    The core keeps 256 events for a slow program, and drops the events after them. Do slow work in
    another thread or process.
    """
    with client.subscribe() as stream:
        for event in stream:
            match event:
                case MessageReceivedEvent(data=message):
                    log.info("Message %s in %s", message.message_id, event.instance)
                case MessageStatusEvent(data=status):
                    log.info("Message %s is %s", status.message_id, status.status)
                case ConnectionUpdateEvent(data=update):
                    log.info("Instance %s is %s", event.instance, update.status)
                case BulkFinishedEvent(data=job):
                    log.info("Bulk job %s is %s", job.job_id, job.status)
                case InvalidPayloadEvent(reason=reason):
                    log.error("Event %s cannot be read: %s", event.id, reason)
                case OtherEvent():
                    log.info("Event %s is not used", event.event)


def main() -> None:
    with CoreClient(
        os.environ["CALYX_CORE_ADDRESS"], os.environ["CALYX_CORE_TOKEN"], insecure=True
    ) as client:
        # The SDK never opens a broken stream again: this loop does. The core delivers each event
        # at most once, so the events between two streams are lost.
        while True:
            try:
                follow(client)
            except CalyxError as error:
                # One event that is not valid ends the stream. A new stream gives the next events.
                fatal = error.retry is RetryAdvice.DO_NOT_RETRY
                if fatal and error.code != ClientCode.INVALID_EVENT:
                    sys.exit(f"{error.code}: {error.detail} ({error.retry})")
                log.warning("The stream ended: %s: %s", error.code, error.detail)
                time.sleep(error.retry_after or 2)


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    main()
