import { Button } from "antd";
import { useNavigate, useRouteError } from "react-router";

import { reloadAfterChunkFailure } from "@/lib/chunk-recovery";

import "./route-error.css";

const ERROR_ICON_PATH =
    "M512 14.208c274.56 0 497.792 223.168 497.792 497.792 0 274.56-223.168 497.792-497.792 497.792C237.44 1009.792 14.208 786.56 14.208 512 14.208 237.44 237.44 14.208 512 14.208z m0 71.104A427.072 427.072 0 0 0 85.312 512 427.072 427.072 0 0 0 512 938.688 427.072 427.072 0 0 0 938.688 512 427.072 427.072 0 0 0 512 85.312z m35.584 628.16v71.104H476.416v-71.04h71.168z m0-474.048v402.944H476.416V239.424h71.168z";

export default function RouteErrorPage() {
    const error = useRouteError();
    const navigate = useNavigate();
    const message = error instanceof Error ? error.message : "页面暂时无法显示";

    return (
        <main className="route-error-page">
            <div aria-hidden="true" className="route-error-ambient" />

            <section aria-labelledby="route-error-title" className="route-error-grid">
                <div className="route-error-lead">
                    <p aria-hidden="true" className="route-error-display">
                        Oops…
                    </p>
                    <h1 className="route-error-headline" id="route-error-title">
                        出现了一些错误
                    </h1>
                    <p className="route-error-summary">页面在加载过程中遇到了一点阻碍。你可以重新加载当前页面，或返回主页继续创作。</p>
                </div>

                <section className="route-error-diagnostic" role="alert">
                    <p className="route-error-label">
                        <svg aria-hidden="true" className="route-error-icon" viewBox="0 0 1024 1024" xmlns="http://www.w3.org/2000/svg">
                            <path d={ERROR_ICON_PATH} fill="currentColor" />
                        </svg>
                        错误原因
                    </p>
                    <p className="route-error-message">{message}</p>
                </section>

                <div className="route-error-actions">
                    <Button className="route-error-secondary" onClick={reloadAfterChunkFailure}>
                        重新加载
                    </Button>
                    <Button className="route-error-primary" onClick={() => navigate("/")} type="primary">
                        <span>返回主页</span>
                        <svg aria-hidden="true" className="route-error-arrow" fill="none" stroke="currentColor" strokeLinecap="square" strokeWidth="1.4" viewBox="0 0 16 16">
                            <path d="M2.5 8h11" />
                            <path d="M9 3.5 13.5 8 9 12.5" />
                        </svg>
                    </Button>
                </div>
            </section>
        </main>
    );
}
