-- Token-bucket rate limiter (DSN-021b).
--
-- The bucket is two Redis fields under one key:
--   tokens  : current token count (float, may be fractional after refill)
--   last_ms : last refill timestamp in ms-since-epoch
--
-- Refill formula: tokens = min(burst, tokens + elapsed_seconds * rate).
-- A request is allowed iff tokens >= cost; on allow we subtract cost.
-- Everything happens inside this one EVAL so concurrent callers can't
-- both see N tokens and both subtract — the script runs atomically on
-- the Redis side.
--
-- KEYS[1] : the bucket key, e.g. "rl:ip:1.2.3.4"
-- ARGV[1] : refill rate (tokens per second), as a number
-- ARGV[2] : burst (max tokens the bucket can hold)
-- ARGV[3] : cost of this request (usually 1)
-- ARGV[4] : now (ms-since-epoch) — pulled from the caller so tests are
--           deterministic and so we don't have to enable replica-script
--           effects on TIME
-- ARGV[5] : ttl_seconds — Redis key expiry so abandoned buckets are
--           swept (longer than the time it takes a full bucket to refill)
--
-- Returns {allowed, remaining_tokens_rounded, retry_after_ms}:
--   allowed         : 1 if the request is allowed, 0 if denied
--   remaining_tokens: floor of the tokens left after the call
--   retry_after_ms  : ms until enough tokens accumulate for the next
--                     request (0 if allowed)

local key  = KEYS[1]
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local now_ms = tonumber(ARGV[4])
local ttl = tonumber(ARGV[5])

local data = redis.call('HMGET', key, 'tokens', 'last_ms')
local tokens = tonumber(data[1])
local last_ms = tonumber(data[2])

if tokens == nil then
  tokens = burst
  last_ms = now_ms
end

local elapsed_ms = now_ms - last_ms
if elapsed_ms < 0 then elapsed_ms = 0 end
local refilled = tokens + (elapsed_ms / 1000) * rate
if refilled > burst then refilled = burst end

local allowed = 0
local retry_after_ms = 0
if refilled >= cost then
  refilled = refilled - cost
  allowed = 1
else
  -- Time (ms) until we'd accumulate enough tokens for one more request.
  local needed = cost - refilled
  retry_after_ms = math.ceil((needed / rate) * 1000)
end

redis.call('HMSET', key, 'tokens', refilled, 'last_ms', now_ms)
redis.call('EXPIRE', key, ttl)

return {allowed, math.floor(refilled), retry_after_ms}
