<?php

declare(strict_types=1);

namespace Calyx\Client\Internal;

use Calyx\Client\CalyxException;
use Generator;

/**
 * Sends the HTTP requests of the backend client. CurlTransport is the real one; the tests use a
 * fake.
 *
 * A transport adds the headers of the protocol: User-Agent, Content-Type with a body, and Accept
 * on the stream. The client adds Authorization and Idempotency-Key to the request.
 *
 * @internal
 */
interface Transport
{
    /**
     * Returns every HTTP answer, 2xx or not. The client gives a non-2xx answer to ErrorDecoder.
     *
     * @throws CalyxException timeout, network_error, or invalid_response for an answer over 32 MiB
     */
    public function send(Request $request): Response;

    /**
     * Yields the events of a Server-Sent Events answer. Before the first event, a non-2xx answer
     * goes to ErrorDecoder, and the stream throws its error. The end of the answer gives
     * stream_failed. The connection closes when the generator is destroyed, for example after a break of the caller.
     *
     * @param float $idleSeconds the longest time with no data; the loop body of the caller is not counted
     *
     * @return Generator<int, SseEvent, mixed, void>
     *
     * @throws CalyxException
     */
    public function stream(Request $request, float $idleSeconds): Generator;
}
