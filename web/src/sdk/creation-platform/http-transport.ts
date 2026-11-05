/**
 * Web 应用的 SDK 传输：复用既有 `http.raw`，因此登录 Cookie、Base URL 与拦截器只在 request.ts 一处。
 * 这是 SDK 目录里唯一允许依赖应用模块的文件；SDK 核心（contract/transport/client）不引用它。
 */
import axios from "axios";

import { http } from "@/services/api/request";

import type { Transport, TransportResponse } from "./transport";

export const webTransport: Transport = {
    async request(call) {
        try {
            const response = await http.raw<TransportResponse["body"]>({
                method: call.method,
                url: call.path,
                data: call.body,
                headers: call.headers,
                signal: call.signal,
                // 业务失败（4xx/5xx）也要拿到信封交给 SDK 解析，不能被 axios 当成异常吞掉 reason。
                validateStatus: () => true,
            });
            return { status: response.status, body: response.data };
        } catch (error) {
            if (axios.isCancel(error)) throw new DOMException("请求已取消", "AbortError");
            throw error;
        }
    },
};
