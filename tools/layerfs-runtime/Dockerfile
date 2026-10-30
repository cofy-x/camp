ARG BASE_IMAGE=ubuntu@sha256:a2d43bffc46670d7108bb3c0468c67c5557c4c92e83e1e34cbff1322ad1b94dc
FROM ${BASE_IMAGE}
COPY layerfs-daemon /usr/local/bin/layerfs-daemon
COPY node /usr/local/bin/node
COPY libraries/ /usr/local/lib/camp/
COPY pnpm/ /opt/camp/pnpm/
COPY --chmod=0555 pnpm.sh /usr/local/bin/pnpm
COPY --chmod=0555 export.mjs /opt/camp/layerfs/export.mjs
ENV LD_LIBRARY_PATH=/usr/local/lib/camp
ENV COREPACK_ENABLE_NETWORK=0
RUN test "$(/usr/local/bin/node --version)" = "v24.16.0" && test "$(/usr/local/bin/pnpm --version)" = "12.11.2"
RUN /usr/bin/setpriv --reuid=501 --regid=20 --clear-groups /usr/local/bin/node --check /opt/camp/layerfs/export.mjs
LABEL camp.runtime="layerfs" camp.layerfs.commit="35d36575c1eb4d23d4b54534a7030d855db890e7"
