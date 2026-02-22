-- Sliding window rate limiter
-- KEYS[1] = rate limit key
-- ARGV[1] = current timestamp (microseconds)
-- ARGV[2] = window size (microseconds)
-- ARGV[3] = max allowed count
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local clearBefore = now - window
redis.call('ZREMRANGEBYSCORE', key, 0, clearBefore)
local count = redis.call('ZCARD', key)
if count < limit then
    redis.call('ZADD', key, now, now .. '-' .. math.random(1000000))
    redis.call('EXPIRE', key, math.ceil(window / 1000000))
    return {1, limit - count - 1}
end
return {0, 0}
