import os
import sys
import time
from pathlib import Path

from calyx_client_sdk import BackendClient, new_idempotency_key
from calyx_client_sdk.models import BulkJobStatus

client = BackendClient(os.environ["CALYX_URL"], os.environ["CALYX_API_KEY"])

# Only people who asked for this message. Never send unsolicited messages.
recipients = [number.strip() for number in os.environ["CALYX_RECIPIENTS"].split(",")]

# One key for each job, stored before the call. The same key gives the same job.
# Calyx forgets a key after 24 hours. After that, the same key sends to everyone again.
key_file = Path("reminder-2026-11-23.key")
if not key_file.exists():
    key_file.write_text(new_idempotency_key())
if time.time() - key_file.stat().st_mtime > 23 * 60 * 60:
    sys.exit("The key is too old. Find the job with list_bulk_jobs.")

job = client.create_bulk_job(
    os.environ["CALYX_INSTANCE_ID"],
    recipients=recipients,
    text="Your appointment is tomorrow at 10:00.",
    idempotency_key=key_file.read_text(),
).data

# The core sends the messages with its own delays. The webhook bulk.finished gives the end too.
while job.status in (BulkJobStatus.QUEUED, BulkJobStatus.RUNNING):
    time.sleep(10)
    job = client.get_bulk_job(job.id)
    print(job.status, f"{job.progress_percent} %")

print("Sent:", job.sent_messages, "Failed or unknown:", job.failed_messages)
