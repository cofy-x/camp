ARG RUST_IMAGE
ARG NODE_IMAGE
ARG DOCKER_IMAGE
FROM ${NODE_IMAGE} AS node-runtime
FROM ${RUST_IMAGE} AS builder
WORKDIR /source
COPY source/ ./
ENV LAYERFS_CONSTRUCTION_WORKERS=1 CARGO_BUILD_JOBS=6
RUN test "$(uname -m)" = aarch64 && test "$(rustc --version | cut -d ' ' -f 2)" = 1.85.1
RUN cargo fetch --locked
RUN --network=none cargo build --offline --locked --release -p layerfs-node -p layerfs-daemon
COPY --from=node-runtime /usr/local/bin/node /usr/local/bin/node
COPY collector/ /collector/
RUN --network=none node /collector/collect-licenses.mjs

FROM ${DOCKER_IMAGE} AS docker
FROM node-runtime
ARG LAYERFS_COMMIT
LABEL camp.runtime="layerfs-worker" camp.layerfs.commit="${LAYERFS_COMMIT}"
COPY --from=docker /usr/local/bin/docker /usr/local/bin/docker
COPY --from=builder /source/target/release/layerfs-daemon /usr/local/bin/layerfs-daemon
COPY --from=builder /source/target/release/liblayerfs_node.so /opt/camp/layerfs/layerfs.node
COPY --from=builder /source/api/ /opt/camp/layerfs/api/
COPY --from=builder /source/LICENSE /opt/camp/layerfs/licenses/LayerFS-LICENSE
COPY --from=builder /licenses/ /opt/camp/layerfs/licenses/
COPY docker-licenses/ /opt/camp/layerfs/licenses/docker-cli/
COPY worker/ /opt/camp/layerfs/worker/
COPY seal.mjs /opt/camp/layerfs/seal.mjs
ENV LAYERFS_NATIVE=/opt/camp/layerfs/layerfs.node LAYERFS_CONSTRUCTION_WORKERS=1
RUN test "$(uname -m)" = aarch64 && test "$(node --version)" = v24.16.0 && test "$(docker --version | cut -d ' ' -f 3)" = '29.4.0,' && docker --version | cut -d ' ' -f 5 | grep '^9d7ad9f'
RUN cd /opt/camp/layerfs/api && npm install --ignore-scripts --offline --package-lock=false --audit=false --fund=false
RUN node /opt/camp/layerfs/seal.mjs
ENTRYPOINT ["node"]
