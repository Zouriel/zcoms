FROM python:3.12-slim

WORKDIR /app

# Pillow's runtime needs a couple of native libs for image handling.
RUN apt-get update \
    && apt-get install -y --no-install-recommends libjpeg62-turbo zlib1g curl \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app.py .

# Bind to localhost only inside the container; the compose file maps 127.0.0.1.
EXPOSE 8099

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8099/health || exit 1

CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8099"]
