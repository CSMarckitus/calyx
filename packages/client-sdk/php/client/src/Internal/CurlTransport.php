<?php

declare(strict_types=1);

namespace Calyx\Client\Internal;

use Calyx\Client\CalyxException;
use Calyx\Client\Generated\ClientCode;
use Calyx\Client\Version;
use CurlHandle;
use CurlMultiHandle;
use Generator;
use LogicException;
use RuntimeException;

/**
 * The transport of the backend client, with ext-curl. Each call has its own curl handle.
 *
 * @internal
 */
final class CurlTransport implements Transport
{
    /** The largest answer body that the SDK reads (CONTRACT part 2 rule 7). */
    public const int MAX_ANSWER_BYTES = 32 << 20;

    private const string USER_AGENT = 'calyx-php-sdk/' . Version::SDK;

    public function send(Request $request): Response
    {
        $answer = new CurlAnswer(self::MAX_ANSWER_BYTES);
        $handle = $this->handle($request, $answer, false);
        curl_exec($handle);
        $code = curl_errno($handle);
        if ($code !== CURLE_OK) {
            throw $this->failure($request, $code, $answer, false, curl_error($handle));
        }

        return $answer->response(self::status($handle));
    }

    /**
     * @return Generator<int, SseEvent, mixed, void>
     */
    public function stream(Request $request, float $idleSeconds): Generator
    {
        $answer = new CurlAnswer(self::MAX_ANSWER_BYTES);
        $handle = $this->handle($request, $answer, true);
        // The multi interface returns between the reads, so the SDK can keep its own idle timer.
        // CURLOPT_LOW_SPEED_* would close a stream that receives only a ping every 15 s.
        $multi = curl_multi_init();
        curl_multi_add_handle($multi, $handle);
        try {
            yield from $this->events($request, $handle, $multi, $answer, $idleSeconds);
        } finally {
            // This also runs after a break of the caller. A transfer that did not end loses its
            // connection.
            curl_multi_remove_handle($multi, $handle);
            curl_multi_close($multi);
        }
    }

    /**
     * @return Generator<int, SseEvent, mixed, void>
     */
    private function events(
        Request $request,
        CurlHandle $handle,
        CurlMultiHandle $multi,
        CurlAnswer $answer,
        float $idleSeconds,
    ): Generator {
        // Read until the first body bytes, so the status and the headers are known.
        $result = $this->wait($multi, $answer, $idleSeconds);
        $status = self::status($handle);
        // A problem answer that broke in this first read also goes to problem().
        if ($status > 0 && ($status < 200 || $status > 299)) {
            throw $this->problem($request, $handle, $multi, $answer, $idleSeconds, $result);
        }
        // Only a break with no status ends the call here. A 2xx answer with bytes goes to the loop,
        // so the events that arrived are yielded before the error.
        if ($status === 0 && $result !== null && $result !== CURLE_OK) {
            throw $this->failure($request, $result, $answer, false);
        }
        $type = strtolower(trim(explode(';', $answer->headers['content-type'][0] ?? '')[0]));
        if ($type !== 'text/event-stream') {
            $detail = "The backend answered {$status} with a body that is not an event stream.";

            throw ErrorDecoder::client(ClientCode::INVALID_RESPONSE, $detail);
        }

        $parser = new SseParser();
        while (true) {
            foreach ($parser->feed($answer->take()) as $event) {
                yield $event;
            }
            if ($result === CURLE_OK) {
                $parser->finish();
            }
            if ($result !== null) {
                throw $this->failure($request, $result, $answer, true);
            }
            // The idle time starts again here, so the loop body of the caller does not count.
            $result = $this->wait($multi, $answer, $idleSeconds);
        }
    }

    /**
     * Reads the rest of an answer that is not 2xx, and returns its error.
     */
    private function problem(
        Request $request,
        CurlHandle $handle,
        CurlMultiHandle $multi,
        CurlAnswer $answer,
        float $idleSeconds,
        ?int $result,
    ): CalyxException {
        while ($result === null) {
            $result = $this->wait($multi, $answer, $idleSeconds);
        }
        // As in Go: a body that broke goes to the decoder as it is, and gives invalid_response.
        if ($answer->tooLarge || $result === CURLE_OPERATION_TIMEDOUT) {
            return $this->failure($request, $result, $answer, true);
        }

        return ErrorDecoder::decode($request, $answer->response(self::status($handle)));
    }

    /**
     * Runs the transfer until new body bytes arrive or the transfer ends.
     *
     * @return int|null the curl result code when the transfer ended, null while it runs
     *
     * @throws CalyxException stream_failed when no body bytes arrive for $idleSeconds
     */
    private function wait(CurlMultiHandle $multi, CurlAnswer $answer, float $idleSeconds): ?int
    {
        $deadline = self::now() + $idleSeconds;
        $bytes = $answer->bytes;
        while (true) {
            curl_multi_exec($multi, $running);
            if ($running === 0) {
                $info = curl_multi_info_read($multi);

                return is_array($info) && is_int($info['result']) ? $info['result'] : CURLE_OK;
            }
            if ($answer->bytes !== $bytes) {
                return null;
            }
            $left = $deadline - self::now();
            if ($left <= 0) {
                $detail = "The pairing stream sent no data for {$idleSeconds} s.";

                throw ErrorDecoder::client(ClientCode::STREAM_FAILED, $detail);
            }
            // select() can return -1 at once when curl has no socket to wait on yet.
            if (curl_multi_select($multi, min($left, 1.0)) === -1) {
                usleep(1_000);
            }
        }
    }

    private function handle(Request $request, CurlAnswer $answer, bool $stream): CurlHandle
    {
        // An empty Expect stops "Expect: 100-continue", which waits for the server before the body.
        $headers = ['User-Agent: ' . self::USER_AGENT, 'Expect:'];
        if ($request->body !== null) {
            $headers[] = 'Content-Type: application/json';
        } elseif (in_array($request->method, ['POST', 'PUT', 'PATCH'], true)) {
            // As Go's net/http: some front ends answer 411 to a POST with no length.
            // CURLOPT_POSTFIELDS => '' would also add a form Content-Type.
            $headers[] = 'Content-Length: 0';
        }
        if ($stream) {
            $headers[] = 'Accept: text/event-stream';
        }
        foreach ($request->headers as $name => $value) {
            $headers[] = "{$name}: {$value}";
        }

        // The client always builds both, so an empty one is a bug of the SDK.
        if ($request->url === '' || $request->method === '') {
            throw new LogicException('The request has no URL or no method.');
        }
        $handle = curl_init() ?: throw new LogicException('curl_init() failed.');
        curl_setopt_array($handle, [
            CURLOPT_URL => $request->url,
            CURLOPT_CUSTOMREQUEST => $request->method,
            CURLOPT_HTTPHEADER => $headers,
            CURLOPT_HEADERFUNCTION => $answer->header(...),
            CURLOPT_WRITEFUNCTION => $answer->write(...),
            // 0 means no time limit. A limit under 1 ms must not become 0.
            CURLOPT_TIMEOUT_MS => $request->timeLimit === null ? 0 : max(1, (int) ceil($request->timeLimit * 1000)),
            // Without it, curl uses signals for the time limit, and a limit under 1 s fails at once.
            CURLOPT_NOSIGNAL => true,
            // A path parameter of ".." stays in the path, as in Go. curl would remove it.
            CURLOPT_PATH_AS_IS => true,
        ]);
        if ($request->body !== null) {
            curl_setopt($handle, CURLOPT_POSTFIELDS, $request->body);
        }

        return $handle;
    }

    /**
     * The error of a transfer that failed. The text of curl goes only to getPrevious(), so the
     * message cannot show a part of the URL.
     *
     * @param bool $connected true when the answer started: a break is then stream_failed on a stream
     * @param string $text the text of curl_error(); the multi interface does not fill it
     */
    private function failure(
        Request $request,
        int $code,
        CurlAnswer $answer,
        bool $connected,
        string $text = '',
    ): CalyxException {
        $key = $request->header('Idempotency-Key');
        $text = $text !== '' ? $text : (curl_strerror($code) ?? "curl error {$code}");
        $cause = new RuntimeException($text, $code);

        return match (true) {
            $answer->tooLarge => ErrorDecoder::client(
                ClientCode::INVALID_RESPONSE,
                'The backend answered with a body that is larger than the SDK reads.',
                $key,
                $cause,
            ),
            $code === CURLE_OPERATION_TIMEDOUT => ErrorDecoder::client(
                ClientCode::TIMEOUT,
                'The backend did not answer in time.',
                $key,
                $cause,
            ),
            $connected => ErrorDecoder::client(ClientCode::STREAM_FAILED, 'The pairing stream broke.', $key, $cause),
            default => ErrorDecoder::client(ClientCode::NETWORK_ERROR, 'The connection to the backend failed.', $key, $cause),
        };
    }

    private static function status(CurlHandle $handle): int
    {
        return curl_getinfo($handle, CURLINFO_RESPONSE_CODE);
    }

    private static function now(): float
    {
        return hrtime(true) / 1e9;
    }
}
