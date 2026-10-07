ARG PREVIS_RUNTIME_IMAGE=canvas-previs-renderer:local
FROM ${PREVIS_RUNTIME_IMAGE} AS probe-runtime
USER root
RUN mkdir -p /probe/lib \
    && cp /usr/bin/ffprobe /probe/ffprobe.bin \
    && cp -L /usr/lib/libSDL3.so.0 /probe/lib/libSDL3.so.0 \
    && ldd /usr/bin/ffprobe | awk '{if ($1 ~ /^\//) print $1; else if ($3 ~ /^\//) print $3}' \
       | while IFS= read -r library; do cp -L "$library" /probe/lib/; done

FROM golang:1.25-bookworm
COPY --from=probe-runtime /probe /opt/previs-probe
RUN printf '#!/bin/sh\nexec /opt/previs-probe/lib/ld-musl-x86_64.so.1 --library-path /opt/previs-probe/lib /opt/previs-probe/ffprobe.bin "$@"\n' > /opt/previs-probe/ffprobe \
    && chmod 755 /opt/previs-probe/ffprobe \
    && /opt/previs-probe/ffprobe -version >/dev/null
ENV CANVAS_FFPROBE_PATH=/opt/previs-probe/ffprobe
WORKDIR /src
