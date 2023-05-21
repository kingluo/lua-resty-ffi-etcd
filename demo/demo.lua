local etcd = require("resty.ffi.etcd")
local inspect = require("inspect")

local _M = {}

function _M.watch()
    local client, rc, err = etcd.new({
        endpoints = {
            "httpbin.local:2379",
        },
    })
    local watch, rc, err = client:watch{
        key = "/apisix/",
        is_prefix = true,
    }
    if not watch then
        ngx.log(ngx.ERR, "rc=", rc, "err=", err)
    end
    local ok, res = watch:recv()
    assert(ok, "watch:recv failed")
    ngx.say(inspect(res))
end

function _M.benchmark_get()
    local cnt = ngx.var.arg_cnt or 10000

    ngx.update_time()
    local t1 = ngx.now()
    for _ = 1,cnt do
        -- TODO
    end
    ngx.update_time()
    local t2 = ngx.now()
    ngx.say("lua-resty-ffi-etcd cnt:", cnt, ", elapsed: ", t2-t1, " secs")
end

return _M
