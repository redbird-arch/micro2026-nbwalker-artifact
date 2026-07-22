import os
import pandas as pd
import matplotlib.pyplot as plt
import numpy as np
from benchmark import get_high_mpki_benchmarks, get_short_name

def harmonic_mean(df: pd.DataFrame) -> float:
    data = df.tolist()
    if not data or any(x <= 0 for x in data):
        return 0.0
    reciprocal_sum = sum(1 / x for x in data)
    return len(data) / reciprocal_sum


def collect_performance_data(benchmark_name: str, input_dir: str) -> float:
    performance_data = 0
    file_path = os.path.join(input_dir, f"{benchmark_name}.csv")
    if not os.path.exists(file_path):
        print(f"Performance data file {file_path} does not exist.")
        return performance_data
    df = pd.read_csv(file_path)
    for _, row in df.iterrows():
        if row.iloc[1] == " driver" and row.iloc[2] == " kernel_time":
            performance_data = row.iloc[3]
            if performance_data == 0:
                print(f"Warning: kernel time for {benchmark_name} is zero.")
                continue
            else:
                break
        if (
            row.iloc[1] == " GPU1.CommandProcessor"
            and row.iloc[2] == " kernel_time (force stop) 0"
        ):
            performance_data = row.iloc[3]
            print(f"Warning: kernel time (force stop) for {benchmark_name} is {performance_data}.")
            break
    return performance_data

def generate_output(
    baseline: pd.DataFrame,
    Opt1: pd.DataFrame,
    Opt2: pd.DataFrame,
    Opt3: pd.DataFrame,
) -> None:
    benchmarks = get_high_mpki_benchmarks()
    
    # Normalize the time
    Opt1["Data"] = [
        (
            baseline["Data"][i] / Opt1["Data"][i]
            if Opt1["Data"][i] != 0 and baseline["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    Opt2["Data"] = [
        (
            baseline["Data"][i] / Opt2["Data"][i]
            if Opt2["Data"][i] != 0 and baseline["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    Opt3["Data"] = [
        (
            baseline["Data"][i] / Opt3["Data"][i]
            if Opt3["Data"][i] != 0 and baseline["Data"][i] != 0
            else 0
        )
        for i in range(len(benchmarks))
    ]
    baseline["Data"] = [1.0 for _ in range(len(benchmarks))]

    # Ave.
    baseline = pd.concat(
        [
            baseline,
            pd.DataFrame(
                {
                    "Benchmark": ["HMean"],
                    "Data": [harmonic_mean(baseline["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    Opt1 = pd.concat(
        [
            Opt1,
            pd.DataFrame(
                {
                    "Benchmark": ["HMean"],
                    "Data": [harmonic_mean(Opt1["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    Opt2 = pd.concat(
        [
            Opt2,
            pd.DataFrame(
                {
                    "Benchmark": ["HMean"],
                    "Data": [harmonic_mean(Opt2["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )
    Opt3 = pd.concat(
        [
            Opt3,
            pd.DataFrame(
                {
                    "Benchmark": ["HMean"],
                    "Data": [harmonic_mean(Opt3["Data"])],
                }
            ),
        ],
        ignore_index=True,
    )

    output_dir = os.path.join(os.environ["PROJECT_ROOT"], "reproduce")
    os.makedirs(output_dir, exist_ok=True)
    
    # Save the data to one CSV files
    output_df = pd.DataFrame(
        {
            "Benchmark": baseline["Benchmark"],
            "Baseline": baseline["Data"],
            "SoftWalker": Opt1["Data"],
            "MPW": Opt2["Data"],
            "NBWalker+AMR": Opt3["Data"],
        }
    )

    output_path = os.path.join(output_dir, "figure_25.csv")
    output_df.to_csv(output_path, index=False)

    print(f"Normalized performance data saved to: {output_path}")
    
    # Set Arial font family
    plt.rcParams["font.family"] = "Arial"
    # For macOS, you might need to explicitly set the font file
    plt.rcParams["font.sans-serif"] = ["Arial"]
    plt.rcParams["mathtext.fontset"] = "custom"
    plt.rcParams["mathtext.rm"] = "Arial"
    plt.rcParams["mathtext.it"] = "Arial:italic"
    plt.rcParams["mathtext.bf"] = "Arial:bold"
    
    plt.figure(figsize=(20, 4.5), dpi=300)
    
    bar_width = 0.15
    r1 = np.arange(len(benchmarks) + 1) * (4 * bar_width + 0.2)
    r2 = [x + bar_width for x in r1]
    r3 = [x + bar_width for x in r2]
    r4 = [x + bar_width for x in r3]
    
    bar1 = plt.bar(
        r1,
        baseline["Data"],
        width=bar_width,
        label="Baseline",
        color="#8D2E2C",
        edgecolor="black",
        linewidth=1.5,
    )
    bar2 = plt.bar(
        r2,
        Opt1["Data"],
        width=bar_width,
        label="SoftWalker",
        color="#C3D9F1",
        edgecolor="black",
        linewidth=1.5,
    )
    bar3 = plt.bar(
        r3,
        Opt2["Data"],
        width=bar_width,
        label="MPW",
        color="#5D73A1",
        edgecolor="black",
        linewidth=1.5,
    )
    bar4 = plt.bar(
        r4,
        Opt3["Data"],
        width=bar_width,
        label="NB-Walker + AMR",
        color="#313A5B",
        edgecolor="black",
        linewidth=1.5,
    )
    
    plt.xlim(min(r1) - bar_width, max(r4) + bar_width)
    plt.xticks(
        [r + 1.5 * bar_width for r in r1],
        [get_short_name(benchmarks[i]) for i in range(len(benchmarks))] + ["HMean"],
        fontsize=36,
        fontweight="bold",
    )
    plt.ylabel("Speedup", fontsize=36, fontweight="bold")
    plt.yticks(
        np.arange(0, 6.1, 2),
        fontsize=36,
        fontweight="bold",
    )
    plt.ylim(0, 6)
    plt.legend(
        loc="upper center",
        ncol=4,
        bbox_to_anchor=(0.5, 1),
        bbox_transform=plt.gcf().transFigure,  # 使用图形坐标系
        frameon=True,
        fancybox=True,
        framealpha=0.7,
        prop={"weight": "bold", "size": 26},
    )
    plt.tight_layout(rect=[0, 0, 1, 0.925])
    plt.grid(axis="y", alpha=0.3)
    plt.axhline(y=1, color="red", linewidth=0.8, linestyle="--")

    ax = plt.gca()

    # 设置图的边框加粗
    for spine in ax.spines.values():
        spine.set_linewidth(1.75)  # 设置边框宽度为 2.5，可根据需要调整

    output_file = os.path.join(
        output_dir, "figure_25"
    )
    plt.savefig(output_file + ".png")
    print(f"Plot saved to {output_file}")

if __name__ == "__main__":
    baseline = pd.DataFrame(columns=["Benchmark", "Data"])
    Opt1     = pd.DataFrame(columns=["Benchmark", "Data"])
    Opt2     = pd.DataFrame(columns=["Benchmark", "Data"])
    Opt3     = pd.DataFrame(columns=["Benchmark", "Data"])

    for benchmark in get_high_mpki_benchmarks():
        def append_row(df, benchmark, input_dir):
            perf_data = collect_performance_data(benchmark_name=benchmark, input_dir=input_dir)
            return pd.concat(
                [df, pd.DataFrame({"Benchmark": [benchmark], "Data": [perf_data]})],
                ignore_index=True,
            )

        baseline = append_row(baseline, benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "Baseline"))
        Opt1     = append_row(Opt1,     benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "SoftWalker"))
        Opt2     = append_row(Opt2,     benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "MPW"))
        Opt3     = append_row(Opt3,     benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "NBWalker+AMR"))

    generate_output(
        baseline=baseline.copy(),
        Opt1=Opt1.copy(),
        Opt2=Opt2.copy(),
        Opt3=Opt3.copy(),
    )