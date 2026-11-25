import os
import sys

from calyx_client_sdk import CalyxError

from calyx_client_sdk_core import CoreClient
from calyx_client_sdk_core.models import PairingCodeSettings, WhatsAppWebSettings

# The class of the provider object chooses the provider. Without `pairing_code`, the core gives QR
# codes: each update has a new QR text, and your program draws it.
provider = WhatsAppWebSettings(
    locale="es-ES",
    pairing_code=PairingCodeSettings(phone_number=os.environ["CALYX_PHONE"]),
)

try:
    with (
        CoreClient(
            os.environ["CALYX_CORE_ADDRESS"], os.environ["CALYX_CORE_TOKEN"], insecure=True
        ) as client,
        client.create_instance(os.environ["CALYX_INSTANCE_NAME"], provider=provider) as stream,
    ):
        # Read to the end. When the program stops before it, the core stops the pairing.
        for update in stream:
            print(update.status, update.message)
            if update.pairing_code is not None:
                # A secret: show it only to the owner of the number.
                print("Type this code on the phone:", update.pairing_code)
except CalyxError as error:
    sys.exit(f"{error.code}: {error.detail} ({error.retry})")

# The stream ended with no error.
print("Connected")
