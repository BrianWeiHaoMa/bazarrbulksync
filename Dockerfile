FROM python:3.13-slim

WORKDIR /app

RUN pip install --no-cache-dir --upgrade pip

RUN pip install --no-cache-dir \
    bazarrbulksync==0.1.0

ENTRYPOINT ["bazarrbulksync"]

CMD ["bazarrbulksync", "--help"]
