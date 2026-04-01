#!/bin/bash

# Run commands that build and push images

# This script is used instead of build/rules.mk
# whenever you need to build multiarch image with
# docker buildx build --platform=smth
# (see Dockerfiles in the root directory)

# ENV variables: 
# * BINARIES (example: "e2e-test")
# * ALL_ARCH (example: "amd64 arm64")
# * REGISTRY (container registry)
# * VERSION (example: "test")

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

# docker buildx is in /root/.docker/cli-plugins/docker-buildx
HOME=/usr/local/google/home/mmamczur

REPO_ROOT=$(git rev-parse --show-toplevel)
cd ${REPO_ROOT}

BINARIES=${BINARIES:-"psc-e2e-test neg-e2e-test ingress-controller-e2e-test"}
ALL_ARCH=${ALL_ARCH:-"amd64 arm64"}
REGISTRY=${REGISTRY:-"gcr.io/example"}
VERSION=${VERSION:-"test"}
ADDITIONAL_TAGS=${ADDITIONAL_TAGS:-""}

echo BINARIES=${BINARIES}
echo ALL_ARCH=${ALL_ARCH}
echo REGISTRY=${REGISTRY}
echo VERSION=${VERSION}
echo ADDITIONAL_TAGS=${ADDITIONAL_TAGS}

echo "building all binaries"
make all-build ALL_ARCH="${ALL_ARCH}" CONTAINER_BINARIES="${BINARIES}"

# To create cross compiled images
echo "setting up docker buildx.."
docker buildx install
docker buildx create --use

# Download crane cli
curl -sL "https://github.com/google/go-containerregistry/releases/download/v0.21.3/go-containerregistry_$(uname -s)_$(uname -m).tar.gz" | tar xvzf - krane

for binary in ${BINARIES}
do
    # "arm64 amd64" ---> "linux/arm64,linux/amd64"
    MULTIARCH_IMAGE="${REGISTRY}/ingress-gce-${binary}:${VERSION}"
    echo "building ${MULTIARCH_IMAGE} image.."

    # build the per arch images
    for arch in ${ALL_ARCH}
    do
      tag="${MULTIARCH_IMAGE}-${arch}"
      docker buildx build -f Dockerfile.${binary} . --tag ${tag} --platform "linux/${arch}" --load
      docker push ${tag}
    done

    # delete the manifest locally, so it won't conflict with a previous cache.
    docker manifest rm ${MULTIARCH_IMAGE} || true
    arch_images=""
    for arch in ${ALL_ARCH}
    do
      arch_images+="${MULTIARCH_IMAGE}-${arch} "
    done
    # create the multiarch manifest and annotate with specific os/arch combination
    docker manifest create --amend ${MULTIARCH_IMAGE} ${arch_images}
    for arch in ${ALL_ARCH}
    do
      docker manifest annotate --os=linux --arch=${arch} ${MULTIARCH_IMAGE} ${MULTIARCH_IMAGE}-${arch}
    done

    docker manifest push ${MULTIARCH_IMAGE}

    # attach the extra tags to all images
    for tag in ${ADDITIONAL_TAGS}
    do
        ./krane tag ${MULTIARCH_IMAGE} ${tag}
        for arch in ${ALL_ARCH}
        do
          ./krane tag ${MULTIARCH_IMAGE}-${arch} ${tag}-${arch}
        done
    done


    echo "done, pushed $MULTIARCH_IMAGE image"

   # Tag arch specific images for the legacy registries
   for arch in ${ALL_ARCH}
   do
       # krane is a variation of crane that supports k8s auth
       ./krane copy --platform linux/${arch} ${MULTIARCH_IMAGE} ${REGISTRY}/ingress-gce-${binary}-${arch}:${VERSION}
       for tag in ${ADDITIONAL_TAGS}
       do
           ./krane tag ${REGISTRY}/ingress-gce-${binary}-${arch}:${VERSION} ${tag}
       done
   done
  echo "images are copied to arch specific registries"
done
