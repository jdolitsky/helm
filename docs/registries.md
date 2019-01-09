# Registries

In Helm 3, you are now able to store charts in any registry that is OCI image friendly.

Registries with known support:

- [Docker Registry](https://hub.docker.com/_/registry) v2.7+ (based on open-source project, [Distribution](https://github.com/docker/distribution))
- [Azure Container Registry](https://azure.microsoft.com/en-us/services/container-registry/)

For more background on this topic, please see
[this post](https://www.opencontainers.org/blog/2018/10/11/oci-image-support-comes-to-open-source-docker-registry).

## For Chart Publishers

Tag chart directory and store in local cache (`~/.helm/registry`):

```
helm tag mychart/ localhost:5000/mychart:0.1.0
```

List all charts in local cache:

```
helm charts
```

Publish a chart from cache to remote:

```
helm push localhost:5000/mychart:0.1.0
```

## For Chart Consumers

Download chart from remote:

```
helm pull localhost:5000/mychart:0.1.0
```

Install chart into Kubernetes cluster:

```
helm install myrelease localhost:5000/mychart:0.1.0
```
