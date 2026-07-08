"""
Embedding Service - Provides vector embeddings for AI agent framework.
This service supports multiple backends: sentence-transformers and Ollama.
"""
import asyncio
import os
import json
import logging
import hashlib
import unicodedata
import re
import requests
import numpy as np
from typing import List, Optional

from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel, Field
from dotenv import load_dotenv

# Load environment variables
load_dotenv()

# Configure logging
logging.basicConfig(
    level=os.getenv("LOG_LEVEL", "INFO"),
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s"
)
logger = logging.getLogger(__name__)

# Initialize FastAPI app
app = FastAPI(
    title="Embedding Service",
    description="Vector embedding service for AI agent framework (supports Ollama)",
    version="2.0.0"
)

# Add CORS middleware
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

# Backend type
BACKEND_TYPE = os.getenv("BACKEND_TYPE", "ollama")  # "ollama" or "transformers"

# Ollama configuration
OLLAMA_BASE_URL = os.getenv("OLLAMA_BASE_URL", "http://localhost:11434")
OLLAMA_MODEL = os.getenv("OLLAMA_MODEL", "qwen3-embedding:0.6b")

# Global variables
model = None
redis_client = None
MODEL_NAME = os.getenv("MODEL_NAME", "qwen3-embedding:0.6b")
EMBEDDING_DIM = int(os.getenv("EMBEDDING_DIM", "1024"))
REDIS_URL = os.getenv("REDIS_URL", "redis://localhost:6379")
CACHE_TTL = int(os.getenv("CACHE_TTL", "86400"))  # 24 hours
BATCH_SIZE = int(os.getenv("BATCH_SIZE", "32"))
MAX_LENGTH = int(os.getenv("MAX_LENGTH", "512"))


class OllamaClient:
    """Client for Ollama embedding API."""
    
    def __init__(self, base_url: str, model: str):
        self.base_url = base_url.rstrip('/')
        self.model = model
    
    def embed(self, text: str) -> List[float]:
        """Generate embedding using Ollama."""
        response = requests.post(
            f"{self.base_url}/api/embeddings",
            json={
                "model": self.model,
                "prompt": text
            },
            timeout=30
        )
        response.raise_for_status()
        return response.json()["embedding"]
    
    def embed_batch(self, texts: List[str]) -> List[List[float]]:
        """Generate embeddings for multiple texts using Ollama."""
        embeddings = []
        for text in texts:
            embedding = self.embed(text)
            embeddings.append(embedding)
        return embeddings
    
    def health(self) -> bool:
        """Check if Ollama is healthy."""
        try:
            response = requests.get(f"{self.base_url}/api/tags", timeout=5)
            return response.status_code == 200
        except Exception:
            return False


# Data models
class EmbedRequest(BaseModel):
    text: str = Field(..., description="Text to embed")
    prefix: str = Field("", description="Prefix for the text (e.g., 'query:', 'passage:')")


class EmbedResponse(BaseModel):
    embedding: List[float] = Field(..., description="Vector embedding")
    dimension: int = Field(..., description="Embedding dimension")
    cached: bool = Field(default=False, description="Whether result was cached")


class BatchEmbedRequest(BaseModel):
    texts: List[str] = Field(..., description="List of texts to embed")
    prefix: str = Field("", description="Prefix for all texts")


class BatchEmbedResponse(BaseModel):
    embeddings: List[List[float]] = Field(..., description="List of vector embeddings")
    dimension: int = Field(..., description="Embedding dimension")
    cached_count: int = Field(default=0, description="Number of cached results")


class HealthResponse(BaseModel):
    status: str = Field(..., description="Service health status")
    model: str = Field(..., description="Loaded model name")
    cache_enabled: bool = Field(..., description="Whether cache is enabled")
    backend_type: str = Field(default="ollama", description="Backend type (ollama/transformers)")


# Startup event
@app.on_event("startup")
async def startup_event():
    """Initialize model and connections on startup."""
    global model, redis_client
    
    logger.info("Starting embedding service...")
    logger.info(f"Backend type: {BACKEND_TYPE}")
    
    try:
        if BACKEND_TYPE == "ollama":
            # Initialize Ollama client
            logger.info(f"Initializing Ollama client: {OLLAMA_BASE_URL}")
            logger.info(f"Ollama model: {OLLAMA_MODEL}")
            
            model = OllamaClient(OLLAMA_BASE_URL, OLLAMA_MODEL)
            
            # Check Ollama health
            if not model.health():
                raise Exception("Ollama is not running or not accessible")
            
            # Test embedding
            test_embedding = model.embed("test")
            logger.info(f"Ollama client initialized successfully, dimension: {len(test_embedding)}")
            
        else:
            # Initialize sentence-transformers model
            logger.info(f"Loading model: {MODEL_NAME}")
            from sentence_transformers import SentenceTransformer
            st_model = SentenceTransformer(MODEL_NAME)
            model = st_model
            logger.info(f"Model loaded successfully, dimension: {model.get_sentence_embedding_dimension()}")
        
        # Initialize Redis cache
        try:
            import redis
            redis_client = redis.from_url(REDIS_URL, decode_responses=False)
            redis_client.ping()
            logger.info("Redis cache connected successfully")
        except Exception as e:
            logger.warning(f"Redis connection failed, running without cache: {e}")
        
        logger.info("Embedding service started successfully")
        
    except Exception as e:
        logger.error(f"Failed to start embedding service: {e}")
        raise


# Shutdown event
@app.on_event("shutdown")
async def shutdown_event():
    """Clean up resources on shutdown."""
    global redis_client
    
    logger.info("Shutting down embedding service...")
    
    if redis_client:
        redis_client.close()
        logger.info("Redis connection closed")
    
    logger.info("Embedding service stopped")


# API endpoints
@app.get("/health", response_model=HealthResponse)
async def health_check():
    """Health check endpoint."""
    model_name = OLLAMA_MODEL if BACKEND_TYPE == "ollama" else MODEL_NAME
    return HealthResponse(
        status="healthy",
        model=model_name,
        cache_enabled=redis_client is not None,
        backend_type=BACKEND_TYPE
    )


@app.post("/embed", response_model=EmbedResponse)
async def embed(request: EmbedRequest):
    """
    Generate vector embedding for a single text.
    
    This endpoint supports model-specific prefixes (e.g., 'query:', 'passage:') for e5 models.
    """
    if not model:
        raise HTTPException(status_code=503, detail="Model not loaded")
    
    # Normalize text to avoid cache miss explosion
    normalized_text = normalize_text(request.text)
    
    # Check cache first
    cache_key = generate_cache_key(normalized_text, request.prefix)
    if redis_client:
        try:
            cached_data = redis_client.get(cache_key)
            if cached_data:
                embedding = json.loads(cached_data)
                return EmbedResponse(
                    embedding=embedding,
                    dimension=len(embedding),
                    cached=True
                )
        except Exception as e:
            logger.warning(f"Cache lookup failed: {e}")
    
    # Add prefix for e5 models
    text_with_prefix = request.prefix + normalized_text if request.prefix else normalized_text
    
    # Truncate if too long
    if len(text_with_prefix) > MAX_LENGTH:
        text_with_prefix = text_with_prefix[:MAX_LENGTH]
    
    # Generate embedding based on backend type
    if BACKEND_TYPE == "ollama":
        raw = await asyncio.to_thread(model.embed, text_with_prefix)
        # Normalize to unit vector for accurate cosine similarity
        embedding = normalize_vector(raw)
    else:
        # sentence-transformers backend
        raw = await asyncio.to_thread(model.encode, text_with_prefix)
        embedding = raw.tolist()
    
    # Cache the result
    if redis_client:
        try:
            redis_client.setex(
                cache_key,
                CACHE_TTL,
                json.dumps(embedding)
            )
        except Exception as e:
            logger.warning(f"Cache write failed: {e}")
    
    return EmbedResponse(
        embedding=embedding,
        dimension=len(embedding),
        cached=False
    )


@app.post("/embed_batch", response_model=BatchEmbedResponse)
async def embed_batch(request: BatchEmbedRequest):
    """
    Generate vector embeddings for multiple texts.
    
    This is more efficient than multiple single embed calls.
    """
    if not model:
        raise HTTPException(status_code=503, detail="Model not loaded")
    
    # Normalize texts
    normalized_texts = [normalize_text(text) for text in request.texts]
    
    texts_with_prefix = [
        request.prefix + text if request.prefix else text
        for text in normalized_texts
    ]
    
    # Truncate if too long
    texts_with_prefix = [
        text[:MAX_LENGTH] if len(text) > MAX_LENGTH else text
        for text in texts_with_prefix
    ]
    
    # Check cache first — only generate embeddings for uncached texts
    cached_count = 0
    embeddings = [None] * len(normalized_texts)
    uncached_indices: List[int] = []
    uncached_texts: List[str] = []

    if redis_client:
        for i, text in enumerate(normalized_texts):
            cache_key = generate_cache_key(text, request.prefix)
            try:
                cached_data = redis_client.get(cache_key)
                if cached_data:
                    embeddings[i] = json.loads(cached_data)
                    cached_count += 1
                else:
                    uncached_indices.append(i)
                    uncached_texts.append(texts_with_prefix[i])
            except Exception as e:
                logger.warning(f"Cache lookup failed for text {i}: {e}")
                uncached_indices.append(i)
                uncached_texts.append(texts_with_prefix[i])
    else:
        uncached_indices = list(range(len(normalized_texts)))
        uncached_texts = texts_with_prefix

    # Generate embeddings only for uncached texts (offloaded to thread)
    if uncached_texts:
        if BACKEND_TYPE == "ollama":
            raw_batch = await asyncio.to_thread(model.embed_batch, uncached_texts)
            new_embeddings = [normalize_vector(e) for e in raw_batch]
        else:
            # sentence-transformers backend
            raw_batch = await asyncio.to_thread(model.encode, uncached_texts)
            new_embeddings = raw_batch.tolist()

        # Merge into result array and cache each new embedding
        for j, idx in enumerate(uncached_indices):
            embeddings[idx] = new_embeddings[j]
            if redis_client:
                cache_key = generate_cache_key(normalized_texts[idx], request.prefix)
                try:
                    redis_client.setex(
                        cache_key,
                        CACHE_TTL,
                        json.dumps(embeddings[idx])
                    )
                except Exception as e:
                    logger.warning(f"Cache write failed for text {idx}: {e}")
    
    return BatchEmbedResponse(
        embeddings=embeddings,
        dimension=len(embeddings[0]) if embeddings else 0,
        cached_count=cached_count
    )


def normalize_vector(vec: list) -> list:
    """
    Normalize vector to unit length for accurate cosine similarity.
    Ollama does not guarantee returned vectors are unit vectors.
    """
    arr = np.array(vec, dtype=np.float64)
    norm = np.linalg.norm(arr)
    if norm > 0:
        arr = arr / norm
    return arr.tolist()


def normalize_text(text: str) -> str:
    """
    Normalize text to avoid cache miss explosion.
    
    This function:
    1. Unicode normalization (NFKC)
    2. Lowercase conversion
    3. Trim whitespace
    4. Remove extra spaces (including unicode spaces)
    5. Remove control characters
    
    Args:
        text: Text content to normalize.
    
    Returns:
        Normalized text string.
    """
    # 1. Unicode normalize
    text = unicodedata.normalize('NFKC', text)
    
    # 2. Lowercase
    text = text.lower()
    
    # 3. Trim spaces
    text = text.strip()
    
    # 4. Remove extra spaces (including unicode spaces)
    # Replace all unicode whitespace with single space
    text = re.sub(r'\s+', ' ', text)
    
    # 5. Remove control characters (except newline and tab)
    text = re.sub(r'[\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\x9f]', '', text)
    
    # 6. Final trim
    text = text.strip()
    
    return text


def generate_cache_key(text: str, prefix: str) -> str:
    """
    Generate cache key for storing embeddings.

    The cache key includes the active model name so that changing the model
    (e.g. OLLAMA_MODEL) invalidates stale entries.

    Args:
        text: Text content to embed.
        prefix: Model-specific prefix.

    Returns:
        Cache key as hex string.
    """
    model_name = OLLAMA_MODEL if BACKEND_TYPE == "ollama" else MODEL_NAME
    key_data = f"{prefix}|{text}|{model_name}"
    hash_obj = hashlib.sha256(key_data.encode())
    return f"embed:{hash_obj.hexdigest()[:16]}"


if __name__ == "__main__":
    import uvicorn
    
    host = os.getenv("HOST", "0.0.0.0")
    port = int(os.getenv("PORT", "8000"))
    
    logger.info(f"Starting embedding service on {host}:{port}")
    uvicorn.run(app, host=host, port=port)