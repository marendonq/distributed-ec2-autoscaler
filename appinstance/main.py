import math
import time
from fastapi import FastAPI
from fastapi.responses import PlainTextResponse

app = FastAPI()

# Registrar el tiempo de inicio para la onda sinusoidal
START_TIME = time.time()

@app.get("/metrics", response_class=PlainTextResponse)
def get_metrics():
    """
    Retorna la carga de CPU simulada en texto plano para máxima eficiencia.
    Genera una onda sinusoidal: rango 10-90, periodo 120s, ruido +-5.
    """
    elapsed = time.time() - START_TIME
    
    # Sinusoidal de 0 a 1 -> math.sin(elapsed * 2 * pi / 120) * 0.5 + 0.5
    # Escalar al rango 10-90: Amplitud = 40, Offset = 50
    base_load = 50.0 + 40.0 * math.sin(elapsed * 2 * math.pi / 120.0)
    
    # Ruido rápido
    noise = 5.0 * math.sin(elapsed * 13.0)
    
    load = base_load + noise
    
    # Limites
    load = max(0.0, min(100.0, load))
    
    # Retornar como texto plano (string del float)
    return f"{load:.2f}"

if __name__ == "__main__":
    import uvicorn
    # Se ejecuta en el puerto 8000 por defecto para que MonitorC lo consuma localmente
    uvicorn.run(app, host="127.0.0.1", port=8000)
