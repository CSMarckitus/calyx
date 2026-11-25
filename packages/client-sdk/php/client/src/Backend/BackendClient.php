<?php

declare(strict_types=1);

namespace Calyx\Client\Backend;

use Calyx\Client\CalyxException;
use Calyx\Client\Generated\BackendCode;
use Calyx\Client\Generated\ClientCode;
use Calyx\Client\Generated\Model\ApiKeyList;
use Calyx\Client\Generated\Model\BulkJob;
use Calyx\Client\Generated\Model\BulkJobList;
use Calyx\Client\Generated\Model\ClientList;
use Calyx\Client\Generated\Model\CreateBulkJobRequest;
use Calyx\Client\Generated\Model\CreateClientRequest;
use Calyx\Client\Generated\Model\CreateInstanceRequest;
use Calyx\Client\Generated\Model\CreateInstanceResult;
use Calyx\Client\Generated\Model\Done;
use Calyx\Client\Generated\Model\Failed;
use Calyx\Client\Generated\Model\Instance;
use Calyx\Client\Generated\Model\InstanceList;
use Calyx\Client\Generated\Model\Me;
use Calyx\Client\Generated\Model\Organization;
use Calyx\Client\Generated\Model\OrgClient;
use Calyx\Client\Generated\Model\Pairing;
use Calyx\Client\Generated\Model\PairInstanceRequest;
use Calyx\Client\Generated\Model\PairResult;
use Calyx\Client\Generated\Model\SendMessageRequest;
use Calyx\Client\Generated\Model\SendMessageResult;
use Calyx\Client\Generated\Model\UpdateClientRequest;
use Calyx\Client\Generated\Model\UpdateInstanceRequest;
use Calyx\Client\Generated\Model\UpdateOrganizationRequest;
use Calyx\Client\Generated\Operations;
use Calyx\Client\IdempotencyKey;
use Calyx\Client\Internal\CurlTransport;
use Calyx\Client\Internal\ErrorDecoder;
use Calyx\Client\Internal\Json;
use Calyx\Client\Internal\Request;
use Calyx\Client\Internal\Transport;
use Calyx\Client\PendingSend;
use Calyx\Client\RetryAdvice;
use Calyx\Client\SendPendingException;
use Calyx\Client\Sent;
use Calyx\Client\Source;
use Generator;
use JsonException;
use JsonSerializable;

/**
 * Calls the HTTP API of the backend (CONTRACT.md part 2). One method for each included
 * operation, named with its operationId.
 *
 * The arguments are the path parameters, the request object, then the named options. A
 * `$timeLimit` is in seconds and replaces the default of the operation. The client never sends a
 * request again by itself.
 */
final class BackendClient
{
    /** The age limit of a pending send: the core forgets a key after 24 hours. */
    private const int PENDING_SEND_SECONDS = 23 * 3600;

    /** How far a firstSentAt can be ahead of this clock. More is a wrong unit, such as ms. */
    private const int CLOCK_AHEAD_SECONDS = 3600;

    /** The calls that make a record and take no key. testdata/create-calls.json has the list too. */
    private const array CREATE_OPERATIONS = ['createClient', 'createInstance'];

    /** The client errors after which the SDK cannot know if the record exists. "aborted" does not occur here. */
    private const array CREATE_CODES = [ClientCode::TIMEOUT, ClientCode::NETWORK_ERROR, ClientCode::INVALID_RESPONSE];

    private readonly string $baseUrl;
    private readonly Transport $transport;

    /**
     * @param string $baseUrl for example "https://calyx.example.com"; http or https only
     * @param string|null $apiKey sent as "Authorization: Bearer <apiKey>"; null sends no header
     * @param Transport|null $transport @internal a test seam
     * @param float $idleSeconds @internal a test seam: the idle limit of the pairing stream
     *
     * @throws CalyxException invalid_config
     */
    public function __construct(
        string $baseUrl,
        #[\SensitiveParameter]
        private readonly ?string $apiKey = null,
        ?Transport $transport = null,
        private readonly float $idleSeconds = 45.0,
    ) {
        // parse_url() reads "localhost:8080" as scheme "localhost", so the scheme is checked by name.
        $parts = parse_url($baseUrl);
        $scheme = is_array($parts) ? strtolower($parts['scheme'] ?? '') : '';
        if (
            !in_array($scheme, ['http', 'https'], true)
            || ($parts['host'] ?? '') === ''
            || preg_match('/[\x00-\x20\x7f]/', $baseUrl) === 1
        ) {
            throw ErrorDecoder::client(
                ClientCode::INVALID_CONFIG,
                'The base URL must start with http:// or https://, have a host, and have no space or control character.',
            );
        }
        if ($apiKey !== null && preg_match('/[\x00-\x1f\x7f]/', $apiKey) === 1) {
            throw ErrorDecoder::client(ClientCode::INVALID_CONFIG, 'The API key has a control character.');
        }
        $this->baseUrl = str_ends_with($baseUrl, '/') ? substr($baseUrl, 0, -1) : $baseUrl;
        $this->transport = $transport ?? new CurlTransport();
    }

    /**
     * What var_dump() and print_r() show. The API key stays out.
     *
     * @return array<string, mixed>
     */
    public function __debugInfo(): array
    {
        return ['baseUrl' => $this->baseUrl, 'apiKey' => $this->apiKey === null ? null : '(hidden)'];
    }

    /** @throws CalyxException */
    public function listAPIKeys(?float $timeLimit = null): ApiKeyList
    {
        return ApiKeyList::fromArray($this->object('listAPIKeys', [], null, $timeLimit));
    }

    /** @throws CalyxException */
    public function revokeAPIKey(string $id, ?float $timeLimit = null): void
    {
        $this->status('revokeAPIKey', $id, $timeLimit);
    }

    /** @throws CalyxException */
    public function listClients(?float $timeLimit = null): ClientList
    {
        return ClientList::fromArray($this->object('listClients', [], null, $timeLimit));
    }

    /**
     * Creates a client.
     *
     * The call has no idempotency key. After a client error (`timeout`, `network_error` or
     * `invalid_response`), the advice is `Unknown`: the backend can have made the client. List the
     * clients before you call again, because a second call can make a duplicate.
     *
     * @throws CalyxException
     */
    public function createClient(CreateClientRequest $request, ?float $timeLimit = null): OrgClient
    {
        return $this->create('createClient', $request, $timeLimit, OrgClient::fromArray(...));
    }

    /** @throws CalyxException */
    public function getClient(string $id, ?float $timeLimit = null): OrgClient
    {
        return OrgClient::fromArray($this->object('getClient', [$id], null, $timeLimit));
    }

    /** @throws CalyxException */
    public function updateClient(string $id, UpdateClientRequest $request, ?float $timeLimit = null): OrgClient
    {
        return OrgClient::fromArray($this->object('updateClient', [$id], $request, $timeLimit));
    }

    /** @throws CalyxException */
    public function deleteClient(string $id, ?float $timeLimit = null): void
    {
        $this->status('deleteClient', $id, $timeLimit);
    }

    /** @throws CalyxException */
    public function listInstances(?float $timeLimit = null): InstanceList
    {
        return InstanceList::fromArray($this->object('listInstances', [], null, $timeLimit));
    }

    /**
     * Creates an instance and starts its pairing.
     *
     * The call has no idempotency key. After a client error (`timeout`, `network_error` or
     * `invalid_response`), the advice is `Unknown`: the backend can have made the instance. List the
     * instances before you call again, because a second call can make a duplicate.
     *
     * @throws CalyxException
     */
    public function createInstance(CreateInstanceRequest $request, ?float $timeLimit = null): CreateInstanceResult
    {
        return $this->create('createInstance', $request, $timeLimit, CreateInstanceResult::fromArray(...));
    }

    /** @throws CalyxException */
    public function getInstance(string $id, ?float $timeLimit = null): Instance
    {
        return Instance::fromArray($this->object('getInstance', [$id], null, $timeLimit));
    }

    /** @throws CalyxException */
    public function updateInstance(string $id, UpdateInstanceRequest $request, ?float $timeLimit = null): Instance
    {
        return Instance::fromArray($this->object('updateInstance', [$id], $request, $timeLimit));
    }

    /**
     * Removes an instance and returns the HTTP status. 202 means that the core did not confirm the
     * removal, and the instance stays in the state "removing". 204 means that it is removed.
     *
     * @throws CalyxException
     */
    public function removeInstance(string $id, ?float $timeLimit = null): int
    {
        return $this->status('removeInstance', $id, $timeLimit);
    }

    /** @throws CalyxException */
    public function pairInstance(string $id, PairInstanceRequest $request, ?float $timeLimit = null): PairResult
    {
        return PairResult::fromArray($this->object('pairInstance', [$id], $request, $timeLimit));
    }

    /** @throws CalyxException */
    public function cancelPairing(string $id, ?float $timeLimit = null): void
    {
        $this->status('cancelPairing', $id, $timeLimit);
    }

    /**
     * Reads the pairing stream of an instance. It yields `update` items, then one `done` or
     * `failed` item, and then the loop ends. An error ends the loop with a CalyxException. A 404
     * on a new follow means that no pairing runs and that the backend keeps no failure. Read the
     * instance. The SDK never opens the stream again (CONTRACT part 9).
     *
     * The call starts when the loop starts to read. The stream has no default time limit, but it has
     * the idle limit of 45 s. Three rules differ from a plain call:
     *
     * - A `$timeLimit` covers the whole stream, and it also counts the time that your loop body
     *   takes (as a context deadline does in Go).
     * - The idle time does not count the time that your loop body takes. Pings wait in the
     *   socket meanwhile.
     * - The connection closes when the generator is destroyed, not at the `break`. After a
     *   `break`, call `unset($stream)` or let the variable leave its scope. The pairing itself
     *   continues until `cancelPairing`.
     *
     * @return Generator<int, PairingItem, mixed, void>
     *
     * @throws CalyxException
     */
    public function followPairing(string $id, ?float $timeLimit = null): Generator
    {
        $request = $this->request('followPairing', [$id], null, $timeLimit, null);
        foreach ($this->transport->stream($request, $this->idleSeconds) as $event) {
            if (!in_array($event->event, ['update', 'done', 'failed'], true)) {
                continue;
            }
            try {
                $data = Json::decode($event->data);
            } catch (JsonException $e) {
                throw ErrorDecoder::client(
                    ClientCode::INVALID_RESPONSE,
                    "The backend sent a \"{$event->event}\" event with data that is not JSON.",
                    null,
                    $e,
                );
            }
            if (!is_array($data)) {
                throw ErrorDecoder::client(
                    ClientCode::INVALID_RESPONSE,
                    "The backend sent a \"{$event->event}\" event with data of the wrong shape.",
                );
            }
            $item = match ($event->event) {
                'update' => new PairingItem('update', update: Pairing::fromArray($data)),
                'done' => new PairingItem('done', done: Done::fromArray($data)),
                default => new PairingItem('failed', failed: Failed::fromArray($data)),
            };
            yield $item;
            if ($event->event !== 'update') {
                return;
            }
        }
    }

    /**
     * Reads the first item of the pairing stream and closes it. The backend sends the latest
     * update first, so the item normally arrives at once.
     *
     * A 404 on the stream means that no pairing runs. Then the state of the instance decides:
     * `CONNECTED` gives Connected. A 404 of the instance or any other status gives Failed with no
     * reason, because the backend deletes a new instance whose pairing fails. When the time limit
     * passes with no item, the answer is Waiting with no pairing. Every other error is thrown.
     *
     * @param float $timeLimit seconds for the whole call, the instance call after a 404 too
     *
     * @throws CalyxException
     */
    public function pairingSnapshot(string $instanceId, float $timeLimit = 2.0): PairingSnapshot
    {
        $start = hrtime(true);
        $stream = $this->followPairing($instanceId, $timeLimit);
        try {
            foreach ($stream as $item) {
                return match ($item->event) {
                    'update' => new PairingSnapshot(PairingState::Waiting, $item->update),
                    'done' => new PairingSnapshot(PairingState::Connected),
                    default => new PairingSnapshot(PairingState::Failed, null, $item->failed?->detail),
                };
            }
        } catch (CalyxException $e) {
            // Release the generator before the second call.
            unset($stream);
            if ($e->source === Source::Client && $e->errorCode === ClientCode::TIMEOUT) {
                return new PairingSnapshot(PairingState::Waiting);
            }
            if ($e->source !== Source::Client && $e->status === 404) {
                return $this->snapshotOfInstance($instanceId, $timeLimit - (hrtime(true) - $start) / 1e9);
            }

            throw $e;
        } finally {
            unset($stream);
        }
        // The stream ended with no item. The parser makes stream_failed for this, so it cannot happen.
        throw ErrorDecoder::client(ClientCode::STREAM_FAILED, 'The pairing stream ended with no item.');
    }

    /** @throws CalyxException */
    private function snapshotOfInstance(string $instanceId, float $timeLeft): PairingSnapshot
    {
        try {
            $instance = $this->getInstance($instanceId, max(0.001, $timeLeft));
        } catch (CalyxException $e) {
            if ($e->source !== Source::Client && $e->status === 404) {
                return new PairingSnapshot(PairingState::Failed);
            }

            throw $e;
        }

        return $instance->status === Instance::STATUS_CONNECTED
            ? new PairingSnapshot(PairingState::Connected)
            : new PairingSnapshot(PairingState::Failed);
    }

    /**
     * Sends one message. The result and every error carry the idempotency key.
     *
     * When the time limit passes, or the backend still runs a send with the same key, the call
     * throws SendPendingException. Store its `pendingSend`, and give it later to checkSend().
     *
     * @return Sent<SendMessageResult>
     *
     * @throws SendPendingException timeout or send_in_progress
     * @throws CalyxException
     */
    public function sendMessage(
        string $id,
        SendMessageRequest $request,
        ?string $idempotencyKey = null,
        ?float $timeLimit = null,
    ): Sent {
        $firstSentAt = time();
        $key = self::sendKey($idempotencyKey);

        return $this->send(new PendingSend($id, $request->to, $request->text, $key, $firstSentAt), $request, $timeLimit);
    }

    /**
     * Sends the request of a pending send again, with the same key. A message that the core
     * sent before gives a Sent with `replayed: true`. The call throws as sendMessage() does, and a
     * new `pendingSend` keeps the `firstSentAt` of the first one.
     *
     * This is a resend, not a lookup. If the first attempt did not send the message, this call
     * sends it now, possibly hours later. The core forgets a key after 24 hours and would then
     * send the message again, so the SDK refuses a pending send older than 23 hours.
     *
     * @return Sent<SendMessageResult>
     *
     * @throws SendPendingException timeout or send_in_progress
     * @throws CalyxException pending_send_expired, with no request; invalid_config for a key
     *                        that the backend refuses, or a firstSentAt more than 1 hour ahead
     */
    public function checkSend(PendingSend $pending, ?float $timeLimit = null): Sent
    {
        // A check never makes a new key: a new key would send a second message.
        self::checkKey($pending->idempotencyKey);
        $age = time() - $pending->firstSentAt;
        if ($age < -self::CLOCK_AHEAD_SECONDS) {
            throw ErrorDecoder::client(
                ClientCode::INVALID_CONFIG,
                'The firstSentAt of the pending send is in the future. It must be in Unix seconds.',
                $pending->idempotencyKey,
            );
        }
        if ($age > self::PENDING_SEND_SECONDS) {
            throw ErrorDecoder::client(
                ClientCode::PENDING_SEND_EXPIRED,
                'The pending send is older than 23 hours. Check the chat before you send it with a new key.',
                $pending->idempotencyKey,
            );
        }

        return $this->send($pending, new SendMessageRequest($pending->to, $pending->text), $timeLimit);
    }

    /**
     * Creates a bulk job. The result and every error carry the idempotency key.
     *
     * @return Sent<BulkJob>
     *
     * @throws CalyxException
     */
    public function createBulkJob(
        string $id,
        CreateBulkJobRequest $request,
        ?string $idempotencyKey = null,
        ?float $timeLimit = null,
    ): Sent {
        $key = self::sendKey($idempotencyKey);

        return new Sent(
            self::withKey($key, fn(): BulkJob => BulkJob::fromArray(
                $this->object('createBulkJob', [$id], $request, $timeLimit, $key),
            )),
            $key,
        );
    }

    /** @throws CalyxException */
    public function listBulkJobs(string $id, ?float $timeLimit = null): BulkJobList
    {
        return BulkJobList::fromArray($this->object('listBulkJobs', [$id], null, $timeLimit));
    }

    /** @throws CalyxException */
    public function getBulkJob(string $id, ?float $timeLimit = null): BulkJob
    {
        return BulkJob::fromArray($this->object('getBulkJob', [$id], null, $timeLimit));
    }

    /** @throws CalyxException */
    public function cancelBulkJob(string $id, ?float $timeLimit = null): BulkJob
    {
        return BulkJob::fromArray($this->object('cancelBulkJob', [$id], null, $timeLimit));
    }

    /** @throws CalyxException */
    public function getMe(?float $timeLimit = null): Me
    {
        return Me::fromArray($this->object('getMe', [], null, $timeLimit));
    }

    /** @throws CalyxException */
    public function getOrganization(?float $timeLimit = null): Organization
    {
        return Organization::fromArray($this->object('getOrganization', [], null, $timeLimit));
    }

    /** @throws CalyxException */
    public function updateOrganization(UpdateOrganizationRequest $request, ?float $timeLimit = null): Organization
    {
        return Organization::fromArray($this->object('updateOrganization', [], $request, $timeLimit));
    }

    /**
     * Runs the sendMessage call of $pending. A timeout or send_in_progress becomes
     * SendPendingException: the message can still go out, so the program must keep the send.
     *
     * @return Sent<SendMessageResult>
     */
    private function send(PendingSend $pending, SendMessageRequest $request, ?float $timeLimit): Sent
    {
        $key = $pending->idempotencyKey;
        try {
            $data = self::withKey($key, fn(): SendMessageResult => SendMessageResult::fromArray(
                $this->object('sendMessage', [$pending->instanceId], $request, $timeLimit, $key),
            ));
        } catch (CalyxException $e) {
            $timeout = $e->source === Source::Client && $e->errorCode === ClientCode::TIMEOUT;
            $inProgress = $e->source === Source::Backend && $e->errorCode === BackendCode::SEND_IN_PROGRESS;
            if ($timeout || $inProgress) {
                throw new SendPendingException($pending, $e);
            }

            throw $e;
        }

        return new Sent($data, $key);
    }

    /**
     * Runs a call and returns the JSON object of its 2xx answer.
     *
     * @param list<string> $params the path parameters
     *
     * @return array<mixed>
     */
    private function object(string $op, array $params, ?JsonSerializable $body, ?float $timeLimit, ?string $key = null): array
    {
        $request = $this->request($op, $params, $body, $timeLimit, $key);
        $response = $this->transport->send($request);
        if ($response->status < 200 || $response->status > 299) {
            throw ErrorDecoder::decode($request, $response);
        }
        try {
            $data = Json::decode($response->body);
        } catch (JsonException $e) {
            throw ErrorDecoder::notJson($request, $response->status, $e);
        }
        if (!is_array($data)) {
            $detail = "The backend answered {$response->status} with JSON that is not an object.";

            throw ErrorDecoder::client(ClientCode::INVALID_RESPONSE, $detail, $key);
        }

        return $data;
    }

    /**
     * Runs a call and returns the HTTP status of its 2xx answer. The body is not read.
     */
    private function status(string $op, string $id, ?float $timeLimit): int
    {
        $request = $this->request($op, [$id], null, $timeLimit, null);
        $response = $this->transport->send($request);
        if ($response->status < 200 || $response->status > 299) {
            throw ErrorDecoder::decode($request, $response);
        }

        return $response->status;
    }

    /**
     * @param list<string> $params
     */
    private function request(string $op, array $params, ?JsonSerializable $body, ?float $timeLimit, ?string $key): Request
    {
        $row = Operations::ALL[$op];
        // Each path has at most one parameter, {id}. rawurlencode() keeps "." and "..": curl is
        // set to keep the path as it is, so they stay in the path.
        $path = str_replace('{id}', rawurlencode($params[0] ?? ''), $row['path']);
        try {
            $json = $body === null ? null : Json::encode($body);
        } catch (JsonException $e) {
            throw ErrorDecoder::client(
                ClientCode::INVALID_CONFIG,
                'The request has a string that is not valid UTF-8.',
                $key,
                $e,
            );
        }
        $headers = [];
        if ($this->apiKey !== null && $this->apiKey !== '') {
            $headers['Authorization'] = 'Bearer ' . $this->apiKey;
        }
        if ($key !== null) {
            $headers['Idempotency-Key'] = $key;
        }

        return new Request(
            $row['method'],
            $this->baseUrl . $path,
            $headers,
            $json,
            $timeLimit ?? $row['timeLimit'],
        );
    }

    /**
     * The key of a send call: the key of the program, or a new one for null or "".
     */
    private static function sendKey(?string $key): string
    {
        if ($key === null || $key === '') {
            return IdempotencyKey::new();
        }
        self::checkKey($key);

        return $key;
    }

    /**
     * Refuses a key that the backend would not get as it is: 1 to 255 bytes, each from 0x21 to
     * 0x7E (the rule of the backend). A header value loses its spaces at the ends, and a line
     * break would let the program add headers. The message never holds the key.
     */
    private static function checkKey(string $key): void
    {
        if (preg_match('/^[\x21-\x7e]{1,255}$/D', $key) !== 1) {
            throw ErrorDecoder::client(
                ClientCode::INVALID_CONFIG,
                'The idempotency key must have 1 to 255 printable ASCII characters, with no space.',
            );
        }
    }

    /**
     * Runs $read. An error that has no key gets the key (CONTRACT part 7 rule 5).
     *
     * @template T
     *
     * @param \Closure(): T $read
     *
     * @return T
     */
    private static function withKey(string $key, \Closure $read): mixed
    {
        try {
            return $read();
        } catch (CalyxException $e) {
            if ($e->idempotencyKey !== null) {
                throw $e;
            }

            throw new CalyxException(
                $e->source,
                $e->errorCode,
                $e->status,
                $e->detail,
                $e->errors,
                $e->requestId,
                $e->retryAfter,
                $key,
                $e->metadata,
                $e->getPrevious(),
                $e->retry,
            );
        }
    }

    /**
     * Runs a create call and reads its answer with $read. A call that makes a record has no
     * idempotency key, so the SDK cannot know if the backend made it after one of these errors.
     * The advice is "unknown" (CONTRACT part 6, "Create calls").
     *
     * @template T
     *
     * @param \Closure(array<mixed>): T $read
     *
     * @return T
     */
    private function create(string $op, JsonSerializable $body, ?float $timeLimit, \Closure $read): mixed
    {
        try {
            return $read($this->object($op, [], $body, $timeLimit));
        } catch (CalyxException $e) {
            $isCreate = in_array($op, self::CREATE_OPERATIONS, true);
            if (!$isCreate || $e->source !== Source::Client || !in_array($e->errorCode, self::CREATE_CODES, true)) {
                throw $e;
            }

            throw new CalyxException(
                $e->source,
                $e->errorCode,
                $e->status,
                $e->detail,
                $e->errors,
                $e->requestId,
                $e->retryAfter,
                $e->idempotencyKey,
                $e->metadata,
                $e->getPrevious(),
                RetryAdvice::Unknown,
            );
        }
    }
}
