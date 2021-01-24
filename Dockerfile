# ######################################################################
# Docker container
# ######################################################################
FROM restreamio/gstreamer:1.18.3-dev-with-source AS build_base

ENV GO111MODULE=on

RUN apt-get update
RUN apt-get install -y xorg-dev  \ 
    libgl1-mesa-dev \
    libopenal1 \ 
    libopenal-dev \
    libvorbis0a \ 
    libvorbis-dev \
    libvorbisfile3 \
    xvfb \
    pkg-config \
    golang-1.14-go \ 
    ca-certificates \
    gcc

ENV PATH=$PATH:/usr/lib/go-1.14/bin

ADD go-gst.tar.gz /go/src/
ADD webg3n-engine.tar.gz /go/src/


WORKDIR /go/src/app

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

RUN mkdir -p /go/bin

RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /go/bin/web-app

# build the actual production image for smaller image size
FROM restreamio/gstreamer:1.18.3-prod
RUN apt-get update
RUN apt-get install -y xorg-dev  \ 
    libgl1-mesa-dev \
    libopenal1 \ 
    libopenal-dev \
    libvorbis0a \ 
    libvorbis-dev \
    libvorbisfile3 \
    xvfb \
    ca-certificates 

COPY --from=build_base /go/bin/web-app /app/web-app

EXPOSE 8000

ENTRYPOINT ["/bin/sh", "-c", "/usr/bin/xvfb-run -s \"-screen 0 1920x1080x24\" -a $@", ""]
CMD ["/app/web-app"]