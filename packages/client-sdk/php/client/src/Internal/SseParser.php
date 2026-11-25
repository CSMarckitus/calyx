<?php

declare(strict_types=1);

namespace Calyx\Client\Internal;

use Calyx\Client\CalyxException;
use Calyx\Client\Generated\ClientCode;
use Calyx\Client\Source;

/**
 * Turns the bytes of an event stream into events. It does no I/O: the caller gives it the
 * chunks that it reads. A port of packages/client-sdk/go/backend/sse.go.
 *
 * @internal
 */
final class SseParser
{
    public const int MAX_EVENT_BYTES = 1 << 20;

    // The line that is now read.
    private string $line = '';
    // True when the last line ended with a CR. A LF that follows belongs to it.
    private bool $afterCr = false;
    // The bytes read for the event now, line terminators included.
    private int $size = 0;
    private string $event = '';
    private ?string $id = null;
    /** @var list<string> */
    private array $data = [];

    public function __construct(
        private readonly int $maxEventBytes = self::MAX_EVENT_BYTES,
    ) {}

    /**
     * Reads a chunk and returns the events that it completes. A frame ends at an empty line.
     * Comments (pings too) and frames without data give nothing.
     *
     * @return list<SseEvent>
     *
     * @throws CalyxException invalid_response when an event is larger than the limit
     */
    public function feed(string $chunk): array
    {
        $events = [];
        $length = strlen($chunk);
        for ($i = 0; $i < $length; $i++) {
            $byte = $chunk[$i];
            // The check runs while the line grows, so a line without an end cannot fill the memory.
            if (++$this->size > $this->maxEventBytes) {
                throw ErrorDecoder::client(ClientCode::INVALID_RESPONSE, 'The backend sent an event that is too large.');
            }
            if ($byte === "\n" && $this->afterCr && $this->line === '') {
                // The CR LF pair that two reads split: skip the LF of the line that already ended.
                $this->afterCr = false;

                continue;
            }
            $this->afterCr = false;
            if ($byte === "\n" || $byte === "\r") {
                // Do not peek: the next byte can wait for a long time.
                $this->afterCr = $byte === "\r";
                $event = $this->endOfLine();
                if ($event !== null) {
                    $events[] = $event;
                }

                continue;
            }
            $this->line .= $byte;
        }

        return $events;
    }

    /**
     * Tells the parser that the stream ended. A partial line or frame is dropped.
     *
     * @throws CalyxException stream_failed, because a pairing stream must end with done or failed
     */
    public function finish(): never
    {
        throw ErrorDecoder::client(ClientCode::STREAM_FAILED, 'The pairing stream ended before done or failed.');
    }

    private function endOfLine(): ?SseEvent
    {
        $line = $this->line;
        $this->line = '';
        if ($line === '') {
            $this->size = 0;
            $event = $this->data === [] ? null : new SseEvent($this->event, implode("\n", $this->data), $this->id);
            $this->event = '';
            $this->id = null;
            $this->data = [];

            return $event;
        }
        if ($line[0] === ':') {
            return null;
        }
        $colon = strpos($line, ':');
        $field = $colon === false ? $line : substr($line, 0, $colon);
        $value = $colon === false ? '' : substr($line, $colon + 1);
        if (str_starts_with($value, ' ')) {
            $value = substr($value, 1);
        }
        match ($field) {
            'event' => $this->event = $value,
            'data' => $this->data[] = $value,
            'id' => $this->id = $value,
            default => null,
        };

        return null;
    }
}
