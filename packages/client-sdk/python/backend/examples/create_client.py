import os
import time

from calyx_client_sdk import BackendClient, CalyxError, RetryAdvice
from calyx_client_sdk.models import OrgClient

calyx = BackendClient(os.environ["CALYX_URL"], os.environ["CALYX_API_KEY"])


def find_or_create(name: str) -> OrgClient:
    # Two clients can have the same name, so your program must use a name one time only.
    for client in calyx.list_clients().items:
        if client.name == name:
            return client
    return calyx.create_client(name=name)


try:
    client = find_or_create("Shop Ana")
except CalyxError as error:
    if error.retry is not RetryAdvice.UNKNOWN:
        raise
    # The answer did not arrive, and the client can exist. A create call has no idempotency key.
    # Give the backend time to finish, and list the clients before a new call.
    time.sleep(15)
    client = find_or_create("Shop Ana")

print("Client id:", client.id)
