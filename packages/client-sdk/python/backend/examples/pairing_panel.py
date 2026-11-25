import logging
import os
import threading
import time
from typing import Literal, TypedDict

from calyx_client_sdk import (
    BackendClient,
    BackendCode,
    CalyxError,
    PairingDone,
    PairingFailed,
    PairingUpdate,
    RetryAdvice,
)
from calyx_client_sdk.models import InstanceStatus

log = logging.getLogger(__name__)
client = BackendClient(os.environ["CALYX_URL"], os.environ["CALYX_API_KEY"])


class PanelState(TypedDict):
    state: Literal["waiting", "connected", "ended", "error"]
    qr_text: str | None


# A dict works in one process. With more processes, store the state in your cache or database.
# The QR text is a secret: do not log it, and show it only to the owner of the number.
states: dict[str, PanelState] = {}


def save(
    instance_id: str,
    state: Literal["waiting", "connected", "ended", "error"],
    qr_text: str | None = None,
) -> PanelState:
    states[instance_id] = PanelState(state=state, qr_text=qr_text)
    return states[instance_id]


def panel_state(instance_id: str) -> PanelState | None:
    """Your web view calls it every 3 seconds, and the page shows the QR code."""
    return states.get(instance_id)


def start_pairing(instance_id: str) -> PanelState:
    """Your web view calls it when a person starts the pairing, and after a restart."""
    try:
        pairing = client.pair_instance(instance_id, pairing="qr").pairing
        state = save(instance_id, "waiting", pairing.qr_text)
    except CalyxError as error:
        if error.code == BackendCode.INSTANCE_CONNECTED:
            return save(instance_id, "connected")
        if error.code != BackendCode.PAIRING_IN_PROGRESS:
            raise
        # Follow the running pairing. Its first item has the QR text of now.
        state = save(instance_id, "waiting")
    # The stream lasts for minutes, so it runs in the background and not in the web view.
    name = f"pairing {instance_id}"
    threading.Thread(target=follow, args=[instance_id], name=name, daemon=True).start()
    return state


def follow(instance_id: str) -> None:
    """Follow the pairing to its end. The SDK never opens a broken stream again: this code does."""
    end: Literal["connected", "ended", "error"] = "error"
    try:
        for _ in range(100):
            try:
                end = wait_for_end(instance_id)
                break
            except CalyxError as error:
                log.warning("Pairing of %s: %s: %s", instance_id, error.code, error.detail)
                if error.retry not in (RetryAdvice.RETRY_NOW, RetryAdvice.RETRY_LATER):
                    break
                time.sleep(error.retry_after or 2)
    finally:
        # Also after an error of your own code, so the page never waits without end.
        save(instance_id, end)


def wait_for_end(instance_id: str) -> Literal["connected", "ended"]:
    try:
        with client.follow_pairing(instance_id) as stream:
            for item in stream:
                match item:
                    case PairingUpdate(pairing=pairing):
                        save(instance_id, "waiting", pairing.qr_text)
                    case PairingDone():
                        return "connected"
                    case PairingFailed(code=code, detail=detail):
                        log.info("Pairing of %s: %s: %s", instance_id, code, detail)
                        return "ended"
    except CalyxError as error:
        if error.code != BackendCode.NOT_FOUND:
            raise

    # No pairing runs and the backend keeps no failure. Read the instance.
    try:
        instance = client.get_instance(instance_id)
    except CalyxError as error:
        if error.code == BackendCode.NOT_FOUND:
            # The backend deletes a new instance when its pairing fails.
            return "ended"
        raise
    return "connected" if instance.status == InstanceStatus.CONNECTED else "ended"
