<?php

declare(strict_types=1);

namespace Calyx\Client\Event;

/**
 * The status values of the typed events. They are strings, not an enum, because a newer core
 * can send a value that this list does not have (CONTRACT part 10 rule 3).
 */
final class Statuses
{
    // connection.update: the InstanceStatus names without INSTANCE_STATUS_.
    public const string CONNECTED = 'CONNECTED';
    public const string CONNECTING = 'CONNECTING';
    public const string NEEDS_AUTH = 'NEEDS_AUTH';
    public const string DISCONNECTED = 'DISCONNECTED';
    public const string ERROR = 'ERROR';

    // bulk.finished: the BulkJobStatus names without BULK_JOB_STATUS_.
    public const string BULK_QUEUED = 'QUEUED';
    public const string BULK_RUNNING = 'RUNNING';
    public const string BULK_COMPLETED = 'COMPLETED';
    public const string BULK_CANCELLED = 'CANCELLED';
    public const string BULK_FAILED = 'FAILED';

    // message.status: the MessageDeliveryStatus names without the prefix, in lower case.
    public const string SENT = 'sent';
    public const string DELIVERED = 'delivered';
    public const string READ = 'read';
    public const string FAILED = 'failed';
}
