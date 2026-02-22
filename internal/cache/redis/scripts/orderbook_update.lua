-- Atomic orderbook level update
-- KEYS[1] = sorted set key (book:{asset}:bids or asks)
-- KEYS[2] = size hash key
-- KEYS[3] = bbo hash key
-- ARGV[1] = price (string)
-- ARGV[2] = size (string)
-- ARGV[3] = side ("bids" or "asks")
local zkey = KEYS[1]
local hkey = KEYS[2]
local bboKey = KEYS[3]
local price = ARGV[1]
local size = tonumber(ARGV[2])
local side = ARGV[3]
if size > 0 then
    redis.call('ZADD', zkey, price, price)
    redis.call('HSET', hkey, price, ARGV[2])
else
    redis.call('ZREM', zkey, price)
    redis.call('HDEL', hkey, price)
end
-- Recompute BBO
if side == 'bids' then
    local top = redis.call('ZREVRANGE', zkey, 0, 0, 'WITHSCORES')
    if #top > 0 then
        redis.call('HSET', bboKey, 'bid', top[2])
    else
        redis.call('HDEL', bboKey, 'bid')
    end
else
    local top = redis.call('ZRANGE', zkey, 0, 0, 'WITHSCORES')
    if #top > 0 then
        redis.call('HSET', bboKey, 'ask', top[2])
    else
        redis.call('HDEL', bboKey, 'ask')
    end
end
return 1
