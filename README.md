# docker-volume-gcs

This is a Docker volume plugin that uses [`gcsfuse`](https://github.com/googlecloudplatform/gcsfuse) to
provision Google Cloud Storage buckets as Docker volumes.

Once this plugin is set up you can access files stored on Google Cloud Storage from inside a Docker
container as follows:

````bash
# Obviously, $bucket must be the name of your bucket.
$ docker run -ti -v $bucket:/data --volume-driver=gcs ubuntu
````

or create the volume with

````bash
# $object is a full GCS object, like $bucket/$object
$ docker volume create --driver=gcs --name=$object
# or to mount a whole bucket, just specify it's name
$ docker volume create --driver=gcs --name=$bucket
````

## Installation

````bash
$ go get github.com/coduno/docker-volume-gcs
````

## Invocation

````bash
$ docker-volume-gcs [gcsfuse options] ROOT
````

The only argument for the plugin is the root directory to used for mounts. It is mandatory
and must be the last argument.

An example invocation would be

````bash
$ sudo docker-volume-gcs --key-file service-account.json --uid $UID --gid $GID --implicit-dirs /var/lib/docker/volumes/gcs
````

## Known issues

Currently, `docker-volume-gcs` must be run as root user, because `/run/docker/plugins` is usually owned by
root and it needs to create it's socket there. `gcsfuse` will complain about being run as root, and you
should pass `--pid` and `--gid` to avoid having everything owned by root.
