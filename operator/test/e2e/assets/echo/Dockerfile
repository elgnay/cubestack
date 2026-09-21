# Base is a build arg so the e2e can point it at the platform registry; upstream
# is the default (see BASE_MIRRORS in the operator Makefile).
ARG PYTHON_BASE=python:3.12-alpine
FROM ${PYTHON_BASE}
COPY server.py /srv/server.py
ENTRYPOINT ["python", "/srv/server.py"]
