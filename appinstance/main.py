from fastapi import FastAPI

app = FastAPI()


@app.get("/")
def root():
    return {"status": "ok", "service": "appinstance"}


@app.get("/health")
def health_check():
    return {"status": "healthy"}