# Interface do laboratório: nginx + o player hls.js, baixado do registro do npm
# na construção da imagem (a página funciona sem internet depois disso).
FROM alpine:3.20 AS vendor
ADD https://registry.npmjs.org/hls.js/-/hls.js-1.5.20.tgz /tmp/hls.tgz
RUN tar -xzf /tmp/hls.tgz -C /tmp package/dist/hls.min.js

FROM nginx:1.27-alpine
COPY --from=vendor /tmp/package/dist/hls.min.js /usr/share/nginx/vendor/hls.min.js
