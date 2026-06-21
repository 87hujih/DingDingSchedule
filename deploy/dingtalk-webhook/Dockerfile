FROM scratch
COPY ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY dingtalk-webhook /dingtalk-webhook
ENTRYPOINT ["/dingtalk-webhook"]
