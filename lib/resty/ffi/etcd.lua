--
-- Copyright (c) 2023, Jinhua Luo (kingluo) luajit.io@gmail.com
-- All rights reserved.
--
-- Redistribution and use in source and binary forms, with or without
-- modification, are permitted provided that the following conditions are met:
--
-- 1. Redistributions of source code must retain the above copyright notice, this
--    list of conditions and the following disclaimer.
--
-- 2. Redistributions in binary form must reproduce the above copyright notice,
--    this list of conditions and the following disclaimer in the documentation
--    and/or other materials provided with the distribution.
--
-- 3. Neither the name of the copyright holder nor the names of its
--    contributors may be used to endorse or promote products derived from
--    this software without specific prior written permission.
--
-- THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
-- AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
-- IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
-- DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
-- FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
-- DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
-- SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
-- CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
-- OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
-- OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
--
local cjson_encode = require("cjson").encode
local cjson_decode = require("cjson").decode
require("resty_ffi")
local etcd = ngx.load_ffi("resty_ffi_etcd")

local CMD_CLIENT_NEW = 0
local CMD_CLIENT_CLOSE = 1
local CMD_WATCH_CREATE = 2
local CMD_WATCH_RECV = 3
local CMD_WATCH_CLOSE = 4

local _M = {}

local objs = {}

ngx.timer.every(3, function()
    if #objs > 0 then
        for _, s in ipairs(objs) do
            local ok = s:close()
            assert(ok)
        end
        objs = {}
    end
end)

local function setmt__gc(t, mt)
    local prox = newproxy(true)
    getmetatable(prox).__gc = function() mt.__gc(t) end
    t[prox] = true
    return setmetatable(t, mt)
end

local meta_watch = {
    __gc = function(self)
        if self.closed then
            return
        end
        table.insert(objs, self)
    end,
    __index = {
        recv = function(self)
            local ok, res, err = etcd:call(cjson_encode({
                cmd = CMD_WATCH_RECV,
                client = self.client,
                req = self.watch,
            }))
            if ok then
                return ok, cjson_decode(res)
            end
            return nil, res, err
        end,
        close = function(self)
            self.closed = true
            return etcd:call(cjson_encode({
                cmd = CMD_WATCH_CLOSE,
                client = self.client,
                req = self.watch,
            }))
        end,
    },
}

local meta_client = {
    __gc = function(self)
        if self.closed then
            return
        end
        table.insert(objs, self)
    end,
    __index = {
        watch = function(self, opts)
            local cmd = {
                cmd = CMD_WATCH_CREATE,
                client = self.client,
                req = opts,
            }
            local ok, res, err = etcd:call(cjson_encode(cmd))
            if ok then
                return setmt__gc({
                    client = self.client,
                    watch = tonumber(res),
                    closed = false
                }, meta_watch)
            end
            return nil, res, err
        end,
        close = function(self)
            local ok = etcd:new(cjson_encode{
                cmd = CMD_CLIENT_CLOSE,
                client = self.client,
            })
            assert(ok)
            self.closed = true
            return ok
        end,
    }
}

function _M.new(opts)
    local ok, client, err = etcd:new(cjson_encode{
        cmd = CMD_CLIENT_NEW,
        req = opts,
    })
    if ok then
        return setmt__gc({
            client = tonumber(client),
            closed = false
        }, meta_client)
    end
    return nil, client, err
end

return _M
