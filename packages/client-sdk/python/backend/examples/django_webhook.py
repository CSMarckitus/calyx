import logging
import os

from django.db import models, transaction
from django.http import HttpRequest, HttpResponse
from django.urls import path
from django.views.decorators.csrf import csrf_exempt
from django.views.decorators.http import require_POST

from calyx_client_sdk import (
    BulkFinishedEvent,
    CalyxError,
    ConnectionUpdateEvent,
    InvalidPayloadEvent,
    MessageReceivedEvent,
    MessageStatusEvent,
    OtherEvent,
    verify_webhook,
)

log = logging.getLogger(__name__)
SECRET = os.environ["CALYX_WEBHOOK_SECRET"]


# Put this class in models.py of your app, without Meta.
class HandledEvent(models.Model):
    id = models.CharField(primary_key=True, max_length=255)

    class Meta:
        app_label = "shop"


# Calyx has no CSRF token. The signature shows who sent the request.
@csrf_exempt
@require_POST
def calyx_webhook(request: HttpRequest) -> HttpResponse:
    try:
        # request.body is the raw body. Parsed JSON does not pass the check.
        event = verify_webhook(request.body, request.headers, SECRET)
    except CalyxError as error:
        log.warning("Calyx webhook refused: %s", error.code)
        return HttpResponse(status=400)

    # When the code in the block fails, the id is not stored, and the next delivery is handled.
    with transaction.atomic():
        _, is_new = HandledEvent.objects.get_or_create(id=event.id)
        if is_new:
            # Calyx waits 5 seconds for the answer. Do slow work in a task.
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
                    log.error("Calyx event %s cannot be read: %s", event.id, reason)
                case OtherEvent():
                    log.info("Event %s is not used", event.event)
    return HttpResponse(status=204)


# Put this list in urls.py. The URL of the webhook in Calyx must be the same, to the last slash.
urlpatterns = [path("calyx/webhook", calyx_webhook)]
