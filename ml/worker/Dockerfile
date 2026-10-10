# hermod-ml worker image.
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    HERMOD_ML_DATA_DIR=/var/lib/hermod-ml \
    HERMOD_ML_ADDR=0.0.0.0:8090

WORKDIR /app

# libgomp is needed by scikit-learn / xgboost OpenMP builds.
RUN apt-get update \
    && apt-get install -y --no-install-recommends libgomp1 \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
RUN pip install --only-binary=:all: -r requirements.txt

COPY hermod_ml ./hermod_ml

RUN groupadd --system --gid 10001 hermod \
    && useradd --system --uid 10001 --gid hermod --no-create-home --shell /usr/sbin/nologin hermod \
    && mkdir -p /var/lib/hermod-ml \
    && chown hermod:hermod /var/lib/hermod-ml

USER hermod
VOLUME ["/var/lib/hermod-ml"]
EXPOSE 8090

# The slim image has no curl, so probe readiness with the standard library.
# The port follows HERMOD_ML_ADDR in case it is overridden at run time.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["python", "-c", "import os, sys, urllib.request; port = os.environ.get('HERMOD_ML_ADDR', '0.0.0.0:8090').rpartition(':')[2]; sys.exit(0 if urllib.request.urlopen(f'http://127.0.0.1:{port}/v2/health/ready', timeout=4).status == 200 else 1)"]

CMD ["python", "-m", "hermod_ml"]
