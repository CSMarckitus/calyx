# Images for the Python client SDK. Every base image is pinned by the digest of its multi-arch index.
# The two images for the tests install the same lock file. Node for pyright comes from the lock file too.

FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b AS python-3.12
COPY requirements-dev.txt /requirements-dev.txt
RUN pip install --no-cache-dir --root-user-action=ignore --require-hashes --requirement /requirements-dev.txt

FROM python:3.14-slim@sha256:9aaa474097d094040649f2162a627c0049b408f52138d980916788d80f04802f AS python-3.14
COPY requirements-dev.txt /requirements-dev.txt
RUN pip install --no-cache-dir --root-user-action=ignore --require-hashes --requirement /requirements-dev.txt

FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b AS python-protoc
COPY requirements-protoc.txt /requirements-protoc.txt
RUN pip install --no-cache-dir --root-user-action=ignore --require-hashes --requirement /requirements-protoc.txt
