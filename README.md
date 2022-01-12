# vSphere Event Streaming

[![Latest
Release](https://img.shields.io/github/release/embano1/vsphere-event-streaming.svg?logo=github&style=flat-square)](https://github.com/embano1/vsphere-event-streaming/releases/latest)
[![go.mod Go
version](https://img.shields.io/github/go-mod/go-version/embano1/vsphere-event-streaming)](https://github.com/embano1/vsphere-event-streaming)

Prototype to show how to transform an existing (SOAP) API into a modern
HTTP/REST streaming API.

## Details

The vSphere Event Streaming server connects to the vCenter event stream,
transforms each event into a standarized [`CloudEvent`](https://cloudevents.io/)
and exposes these as *JSON* objects via a (streaming) HTTP/REST API.

The benefits of this approach are:

- Simplified consumption via a modern HTTP/REST API instead of directly using
  vCenter SOAP APIs
- Kubernetes inspired `watch` style to stream events from a specific position
  (or "now")
- Decoupling from vCenter by proxying multiple clients onto the (cached) event
  stream of this streaming server
- Apache [Kafka](https://kafka.apache.org/) inspired behavior, using `Offsets`
  to traverse and replay the event stream
- Lightweight and stateless: events are stored (cached) in memory. If the server
  crashes it resumes from the configurable `VCENTER_STREAM_BEGIN` (default: last
  10 minutes)

The unique event `ID` of each vSphere event is mapped to the position (`Offset`)
in the internal (immutable) event *Log* (journal). Clients use these event `IDs`
(`Offsets`) to read/stream/replay events as JSON objects.

### Example

This example uses `curl` and `jq` to query against a locally running vSphere
Event Streaming server. 

💡 If the server is running in Kubernetes (see below), use the `kubectl
port-forward` command to forward CLI commands to streaming server running in a
Kubernetes cluster.

```console
# get current available event range
$ curl -N -s localhost:8080/api/v1/range | jq .
{
  "earliest": 44,
  "latest": 46
}

# read first available event
$ curl -N -s localhost:8080/api/v1/events/44 | jq .
{
  "specversion": "1.0",
  "id": "44",
  "source": "https://localhost:8989/sdk",
  "type": "vmware.vsphere.UserLoginSessionEvent.v0",
  "datacontenttype": "application/json",
  "time": "2022-01-14T13:26:22.0854137Z",
  "data": {
    "Key": 44,
    "ChainId": 44,
    "CreatedTime": "2022-01-14T13:26:22.0854137Z",
    "UserName": "user\n",
    "Datacenter": null,
    "ComputeResource": null,
    "Host": null,
    "Vm": null,
    "Ds": null,
    "Net": null,
    "Dvs": null,
    "FullFormattedMessage": "User user\n@172.17.0.1 logged in as Go-http-client/1.1",
    "ChangeTag": "",
    "IpAddress": "172.17.0.1",
    "UserAgent": "Go-http-client/1.1",
    "Locale": "en_US",
    "SessionId": "56c95aca-aed7-471d-b69f-be73468a89aa"
  },
  "eventclass": "event"
}

# watch for new events and use a jq field selector
$ curl -N -s localhost:8080/api/v1/events\?watch=true | jq '.eventclass+":"+.id+" "+.type'
"event:47 vmware.vsphere.VmStartingEvent.v0"
"event:48 vmware.vsphere.VmPoweredOnEvent.v0"

# start watch from specific id (offset)
curl -N -s localhost:8080/api/v1/events\?watch=true\&offset=44 | jq '.eventclass+":"+.id+" "+.type'
"event:44 vmware.vsphere.UserLoginSessionEvent.v0"
"event:45 vmware.vsphere.VmStoppingEvent.v0"
"event:46 vmware.vsphere.VmPoweredOffEvent.v0"
"event:47 vmware.vsphere.VmStartingEvent.v0"
"event:48 vmware.vsphere.VmPoweredOnEvent.v0"
```

💡 To retrieve the last 50 events use `curl -N -s localhost:8080/api/v1/events`.
The current hardcoded page size is `50` and a pagination API is on my `TODO`
list 🤓

## Deployment

The vSphere Event Streaming server is packaged as a Kubernetes `Deployment` and
configured via environment variables and a Kubernetes `Secret` holding the
vCenter Server credentials.

Requirements:

- Service account/role to read vCenter events
- Kubernetes cluster to deploy the vSphere Event Stream server
- Kubernetes CLI `kubectl` installed
- Network access to vCenter from within the Kubernetes cluster

💡 The vCenter Simulator
[`vcsim`](https://github.com/vmware/govmomi/tree/master/vcsim) can be used to
deploy a mock vCenter in Kubernetes for testing and experimenting.

### Create Credentials

```console
# create namespace
kubectl create namespace vcenter-stream

# create Kubernetes secret holding the vSphere credentials
# replace with your values
kubectl --namespace vcenter-stream create secret generic vsphere-credentials --from-literal=username=user --from-literal=password=pass
```

**Optional** if you want to use `vcsim`:

```console
# deploy container
kubectl --namespace vcenter-stream create -f https://raw.githubusercontent.com/vmware-samples/vcenter-event-broker-appliance/development/vmware-event-router/deploy/vcsim.yaml

# in a separate terminal start port-forwarding
kubectl --namespace vcenter-stream port-forward deploy/vcsim 8989:8989
```

### Change `Deployment` Manifest

Download the latest deployment manifest (`release.yaml`) file from the Github
release page and update the environment variables in `release.yaml` to match
your setup. Then save the file under the same name (to follow along with the
commands).

💡 The environment variables are explained below.

Example Download with `curl`:

```console
curl -L -O https://github.com/embano1/vsphere-event-streaming/releases/latest/download/release.yaml
```

#### vSphere Settings

These settings are required to set up the connection between the event streaming
server and VMware vCenter Server.

💡 If you are not making any modifications to the application leave the default
value for `VCENTER_SECRET_PATH`.

| Variable              | Description                                                                         | Required | Example                           | Default                   |
|-----------------------|-------------------------------------------------------------------------------------|----------|-----------------------------------|---------------------------|
| `VCENTER_URL`         | vCenter Server URL                                                                  | yes      | `https://myvc-01.prod.corp.local` | (empty)                   |
| `VCENTER_INSECURE`    | Ignore vCenter Server certificate warnings                                          | no       | `"true"`                          | `"false"`                 |
| `VCENTER_SECRET_PATH` | Directory where `username` and `password` files are located to retrieve credentials | yes      | `"./"`                            | `"/var/bindings/vsphere"` |

#### Streaming Settings

These settings are used to customize the event streaming server. The event
streaming server internally uses `memlog` as an append-only *Log*. See the
[project](https://github.com/embano1/memlog) for details.

If you want to account for longer downtime you might want to increase the
default value of `5m` used to replay vCenter events after starting the server.

The defaults for `LOG_MAX_RECORD_SIZE_BYTES` and `LOG_MAX_SEGMENT_SIZE` are
usually fine. The total number of records in the internal event *Log* is twice
the `LOG_MAX_SEGMENT_SIZE` (*active* and *history* segment). If the *active*
segment is full and there is already a *history* segment, this *history segment*
will be purged, i.e. events deleted from the internal *Log* (but not within
vCenter Server!).

Trying to read a purged event throws an `invalid offset` error.

💡 If you are seeing the server crashing with out of memory errors (`OOM`), try
increasin the specified memory `limit` in the `release.yaml` manifest.

| Variable                    | Description                                                                                                                    | Required | Example        | Default                                                        |
|-----------------------------|--------------------------------------------------------------------------------------------------------------------------------|----------|----------------|----------------------------------------------------------------|
| `VCENTER_STREAM_BEGIN`      | Stream vCenter events starting at "now" minus specified duration (requires suffix, e.g. `s`/`m`/`h` for seconds/minutes/hours) | yes      | `"1h"`         | `"5m"` (stream starts with events from last 5 minutes)         |
| `LOG_MAX_RECORD_SIZE_BYTES` | Maximum size of each record in the log                                                                                         | yes      | `"1024"` (1Kb) | `"524288"` (512Kb)                                             |
| `LOG_MAX_SEGMENT_SIZE`      | Maximum number of records per segment                                                                                          | yes      | `"10000"`      | `"1000"` (1000 entries in *active*, 1000 in *history* segment) |

### Deploy the Server

```console
kubectl -n vcenter-stream create -f release.yaml
```

Verify that the server is correctly starting:

```console
kubectl -n vcenter-stream logs deploy/vsphere-event-stream
2022-01-14T14:31:43.798Z        INFO    eventstream     server/main.go:166      starting http listener  {"port": 8080}
2022-01-14T14:31:43.874Z        INFO    eventstream     server/main.go:104      starting vsphere event collector        {"begin": "5m0s", "pollInterval": "1s"}
2022-01-14T14:31:44.877Z        DEBUG   eventstream     server/main.go:122      initializing new log    {"startOffset": 27, "maxSegmentSize": 1000, "maxRecordSize": 524288}
2022-01-14T14:31:44.878Z        DEBUG   eventstream     server/main.go:155      wrote cloudevent to log {"offset": 27, "event": "Context Attributes,\n  specversion: 1.0\n  type: vmware.vsphere.UserLoginSessionEvent.v0\n  source: https://vcsim.vcenter-stream.svc.cluster.local/sdk\n  id: 27\n  time: 2022-01-14T14:31:43.7884757Z\n  datacontenttype: application/json\nExtensions,\n  eventclass: event\nData,\n  {\n    \"Key\": 27,\n    \"ChainId\": 27,\n    \"CreatedTime\": \"2022-01-14T14:31:43.7884757Z\",\n    \"UserName\": \"user\",\n    \"Datacenter\": null,\n    \"ComputeResource\": null,\n    \"Host\": null,\n    \"Vm\": null,\n    \"Ds\": null,\n    \"Net\": null,\n    \"Dvs\": null,\n    \"FullFormattedMessage\": \"User user@10.244.0.6 logged in as Go-http-client/1.1\",\n    \"ChangeTag\": \"\",\n    \"IpAddress\": \"10.244.0.6\",\n    \"UserAgent\": \"Go-http-client/1.1\",\n    \"Locale\": \"en_US\",\n    \"SessionId\": \"72096903-7acb-476f-bb76-29941a91fa1b\"\n  }\n", "bytes": 646}
```

💡 If you delete (kill) the Kubernetes `Pod` of the vSphere Event Stream server
to simulate an outage, Kubernetes will automatically restart the server. You
will then be able to query the events starting off of the specified interval
defined via `VCENTER_STREAM_BEGIN`. Events within this timeframe will not be
lost and clients can restart their `watch` from the `earliest` event retrieved
via the `/api/v1/range` endpoint.

### Set up Port-Forwarding

Inside Kubernetes the server is configured with a Kubernetes `Service` and
accessible over the `Service` port `80` within the cluster.

To query the server from a local (remote) machine it is the easiest to create a
port-forwarding.

```console
# forward local port 8080 to service port 80
kubectl -n vcenter-stream port-forward service/vsphere-event-stream 8080:80
```

Then in a separate terminal run `curl` as usual.

```console
curl -s -N localhost:8080/api/v1/range
{"earliest":27,"latest":27}
```

## Uninstall

To uninstall the vSphere Event Stream server and all its dependencies, run:

```console
kubectl delete namespace vcenter-stream
```

## Build Custom Image

**Note:** This step is only required if you made code changes to the Go code.

This example uses [`ko`](https://github.com/google/ko) to build and push
container artifacts.

```console
# only when using kind: 
# export KIND_CLUSTER_NAME=kind
# export KO_DOCKER_REPO=kind.local

export KO_DOCKER_REPO=my-docker-username
export KO_COMMIT=$(git rev-parse --short=8 HEAD)
export KO_TAG=$(git describe --abbrev=0 --tags)

# build, push and run the worker in the configured Kubernetes context 
# and vmware-preemption Kubernetes namespace
ko resolve -BRf config | kubectl -n vcenter-stream apply -f -
```

To delete the deployment:

```console
ko -n vcenter-stream delete -f config
```
