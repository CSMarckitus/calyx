<?php

declare(strict_types=1);

namespace Calyx\Client\Internal;

use CurlHandle;

/**
 * Collects the headers and the body of one curl transfer, through the curl callbacks.
 *
 * @internal
 */
final class CurlAnswer
{
    /** @var array<string, list<string>> */
    public array $headers = [];
    /** The body that is not taken yet. */
    public string $body = '';
    /** The body bytes received since the start. The stream uses it to see new data. */
    public int $bytes = 0;
    /** True when the body that is kept went over the limit. */
    public bool $tooLarge = false;

    public function __construct(
        private readonly int $maxBytes,
    ) {}

    /** CURLOPT_HEADERFUNCTION: one header line for each call. */
    public function header(CurlHandle $handle, string $line): int
    {
        if (str_starts_with($line, 'HTTP/')) {
            // A new status line, for example after "100 Continue": the old headers do not count.
            $this->headers = [];
        } elseif (($colon = strpos($line, ':')) !== false) {
            $name = strtolower(trim(substr($line, 0, $colon)));
            $this->headers[$name][] = trim(substr($line, $colon + 1));
        }

        return strlen($line);
    }

    /** CURLOPT_WRITEFUNCTION: a return value other than the length stops the transfer. */
    public function write(CurlHandle $handle, string $data): int
    {
        if (strlen($this->body) + strlen($data) > $this->maxBytes) {
            $this->tooLarge = true;

            return -1;
        }
        $this->body .= $data;
        $this->bytes += strlen($data);

        return strlen($data);
    }

    /** Returns the body that is not taken yet, and forgets it. */
    public function take(): string
    {
        $body = $this->body;
        $this->body = '';

        return $body;
    }

    public function response(int $status): Response
    {
        return new Response($status, $this->headers, $this->body);
    }
}
