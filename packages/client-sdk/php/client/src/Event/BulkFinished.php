<?php

declare(strict_types=1);

namespace Calyx\Client\Event;

/** The payload of "bulk.finished". */
final readonly class BulkFinished
{
    /**
     * @param string      $jobId          Identifies the bulk job.
     * @param string      $status         The final status of the job. See Statuses. The value is open.
     * @param int         $totalMessages  The number of messages in the job.
     * @param int         $sentMessages   The number of messages that the core sent.
     * @param int         $failedMessages The number of messages that failed.
     * @param string|null $error          The reason of a failed job. Null when the job has no error.
     */
    public function __construct(
        public string $jobId,
        public string $status,
        public int $totalMessages,
        public int $sentMessages,
        public int $failedMessages,
        public ?string $error,
    ) {}

    /**
     * @param array<mixed> $data
     */
    public static function fromArray(array $data): self
    {
        $r = new PayloadReader(EventNames::BULK_FINISHED, $data);

        return new self(
            $r->string('jobId'),
            $r->string('status'),
            $r->int('totalMessages'),
            $r->int('sentMessages'),
            $r->int('failedMessages'),
            $r->stringOrNull('error'),
        );
    }
}
