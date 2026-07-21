benchmarks = [
    "simpleconvolution",
    "jacobi2d",
    "fastwalshtransform",
    "jacobi1d",
    "shoc-reduction",
    "kmeans",
    "stencil2d",
    "pagerank",
    "matrixtranspose",
    "spmv",
    "gups",
    "gesummv",
]

low_mpki_benchmarks = [
    "simpleconvolution",
    "jacobi2d",
    "fastwalshtransform",
    "jacobi1d",
    "shoc-reduction",
]

high_mpki_benchmarks = [
    "kmeans",
    "stencil2d",
    "pagerank",
    "matrixtranspose",
    "spmv",
    "gups",
    "gesummv",
]


memory_overhead = {
    "fastwalshtransform": 16384,
    "gesummv": 49152,
    "gups": 32768,
    "jacobi1d": 49152,
    "jacobi2d": 49152,
    "kmeans": 32768,
    "matrixtranspose": 32768,
    "pagerank": 32768,
    "shoc-reduction": 49152,
    "simpleconvolution": 49152,
    "spmv": 49152,
    "stencil2d": 49152,
}


def get_benchmarks() -> list:
    """
    Returns a list of benchmarks.
    """
    return benchmarks

def get_high_mpki_benchmarks() -> list:
    """
    Returns a list of high MPKI benchmarks.
    """
    return high_mpki_benchmarks

def get_low_mpki_benchmarks() -> list:
    """
    Returns a list of low MPKI benchmarks.
    """
    return low_mpki_benchmarks


def get_short_name(benchmark: str) -> str:
    """
    Returns a short name for each benchmark.
    """
    dict_short_names = {
        "fastwalshtransform": "FWT",
        "gups": "GUPS",
        "jacobi1d": "J1D",
        "jacobi2d": "J2D",
        "kmeans": "KM",
        "matrixtranspose": "MT",
        "pagerank": "PR",
        "simpleconvolution": "SC",
        "shoc-reduction": "RED",
        "spmv": "SPMV",
        "stencil2d": "ST",
        "gesummv": "GEV",
    }

    return dict_short_names[benchmark]