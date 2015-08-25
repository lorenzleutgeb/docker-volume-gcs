# gcs-docker-volume

This is a Docker volume plugin that uses [`gcsfuse`](https://github.com/googlecloudplatform/gcsfuse) to
provision Google Cloud Storage buckets as Docker volumes.

Once this plugin is set up you can access files stored on Google Cloud Storage from inside a Docker
container as follows:

````bash
# Obviously, $bucket must be the name of your bucket.
$ docker run -ti -v $bucket:/data --volume-driver=gcs ubuntu
````

The plugin will do reference counting on the mounted buckets and mount/unmount them as needed.

## Installation

````bash
$ go get github.com/coduno/gcs-docker-volume
````

## Invocation

If you are running this on Google Compute Engine, `gcsfuse` will automatically fall back to default
credentials so you are good to go with:

````bash
$ sudo gcs-docker-volume
````

Otherwise, you will need to point to a JSON Web Token obtained from the Google Developers Console
to gain authorization using a Service Account:

````bash
$ sudo gcs-docker-volume -jwt .../path/to/service-account.json
````

You can adjust the root directory for all mount points as follows:

````bash
$ sudo gcs-docker-volume -root .../path/to/mountpoints
````
