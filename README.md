# VNC recorder

Record [VNC] screens to mp4 video using [ffmpeg]. Thanks to
[amitbet for providing his vnc2video](https://github.com/amitbet/vnc2video)
library which made this wrapper possible.

## Features

- Records VNC screens to MP4 using ffmpeg/libx264
- Automatic reconnection with exponential backoff on VNC disconnection
- Graceful handling of resolution changes (reconnects automatically)
- File splitting by time interval
- S3-compatible upload with retry logic
- Structured logging with level-based policy (TRACE, DEBUG, INFO, WARN, ERROR, FATAL)

## Use

    docker run -it ghcr.io/aluvare/vnc-recorder/vnc-recorder:<release> --help

    NAME:
       vnc-recorder - Connect to a vnc server and record the screen to a video.

    USAGE:
       vnc-recorder [global options] command [command options] [arguments...]

    VERSION:
       0.5.0

    GLOBAL OPTIONS:
       --ffmpeg value              Which ffmpeg executable to use (default: "ffmpeg") [$VR_FFMPEG_BIN]
       --host value                VNC host (default: "localhost") [$VR_VNC_HOST]
       --port value                VNC port (default: 5900) [$VR_VNC_PORT]
       --password value            Password to connect to the VNC host [$VR_VNC_PASSWORD]
       --framerate value           Framerate to record (default: 30) [$VR_FRAMERATE]
       --crf value                 Constant Rate Factor (CRF) to record with (default: 35) [$VR_CRF]
       --outfile value             Output file to record to. (default: "output") [$VR_OUTFILE]
       --splitfile value           Minutes to split file. (default: 0) [$VR_SPLIT_OUTFILE]
       --s3_endpoint value         S3 endpoint. [$VR_S3_ENDPOINT]
       --s3_accessKeyID value      S3 access key id. [$VR_S3_ACCESSKEY]
       --s3_secretAccessKey value  S3 secret access key. [$VR_S3_SECRETACCESSKEY]
       --s3_bucketName value       S3 bucket name. [$VR_S3_BUCKETNAME]
       --s3_region value           S3 region. (default: "us-east-1") [$VR_S3_REGION]
       --s3_ssl                    S3 SSL. (default: false) [$VR_S3_SSL]
       --debug                     Enable debug logging. (default: false) [$VR_DEBUG]
       --help, -h                  show help (default: false)
       --version, -v               print the version (default: false)

**Note:** If you run vnc-recorder from your command line and don't use [docker]
you might want to customize the `--ffmpeg` flag to point to an existing
[ffmpeg] installation.

## Environment Variables (Logging)

| Variable | Values | Default | Description |
|---|---|---|---|
| `LOG_LEVEL` | `trace`, `debug`, `info`, `warn`, `error`, `fatal` | `info` | Minimum log level |
| `LOG_DIR` | path | `<binary_dir>/logs` | Root directory for log files |
| `LOG_MAX_SIZE` | bytes | `10485760` (10 MiB) | Threshold for size-based rotation |
| `LOG_STDOUT_ONLY` | `true`/`false` | auto-detected | Stdout only, no files. Auto-enabled in Docker |

## Build

    docker build -t yourbuild .
    docker run -it yourbuild --help

## Error Handling

- **VNC disconnection**: Automatically reconnects with exponential backoff (1s -> 2s -> 4s -> ... -> 30s max).
- **Resolution changes**: Detected via EOF, triggers reconnect to adapt to new resolution.
- **S3 upload failures**: Retried up to 3 times with exponential backoff. Failed files are kept on disk.
- **Encoder errors**: Current file is saved, session restarts with a new VNC connection.
- **Signals** (SIGINT, SIGTERM, SIGHUP, SIGQUIT): Graceful shutdown — encoder is closed, pending file is uploaded.

[ffmpeg]: https://ffmpeg.org
[docker]: https://www.docker.com
[vnc]: https://en.wikipedia.org/wiki/Virtual_Network_Computing
