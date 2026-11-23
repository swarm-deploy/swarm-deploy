FROM alpine:3.23

RUN apk add --no-cache git-daemon \
    && git config --system --add safe.directory /srv/git/stacks.git

ENTRYPOINT ["git"]
