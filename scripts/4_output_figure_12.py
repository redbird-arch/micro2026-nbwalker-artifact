import os
import pandas as pd
import matplotlib.pyplot as plt
import numpy as np
from benchmark import get_benchmarks,get_high_mpki_benchmarks, get_low_mpki_benchmarks, get_short_name

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

def generate_output_csv(
    baseline: pd.DataFrame,
    Opt1: pd.DataFrame,
    Opt2: pd.DataFrame,
    Opt3: pd.DataFrame,
) -> None:
    benchmarks = get_benchmarks()
    
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
    
    low_mpki_benchmark_baseline = baseline[baseline["Benchmark"].isin(get_low_mpki_benchmarks())]
    low_mpki_benchmark_Opt1 = Opt1[Opt1["Benchmark"].isin(get_low_mpki_benchmarks())]
    low_mpki_benchmark_Opt2 = Opt2[Opt2["Benchmark"].isin(get_low_mpki_benchmarks())]
    low_mpki_benchmark_Opt3 = Opt3[Opt3["Benchmark"].isin(get_low_mpki_benchmarks())]
    
    high_mpki_benchmark_baseline = baseline[baseline["Benchmark"].isin(get_high_mpki_benchmarks())]
    high_mpki_benchmark_Opt1 = Opt1[Opt1["Benchmark"].isin(get_high_mpki_benchmarks())]
    high_mpki_benchmark_Opt2 = Opt2[Opt2["Benchmark"].isin(get_high_mpki_benchmarks())]
    high_mpki_benchmark_Opt3 = Opt3[Opt3["Benchmark"].isin(get_high_mpki_benchmarks())]

    # Ave.
    baseline = pd.concat(
        [
            baseline,
            pd.DataFrame(
                {
                    "Benchmark": ["Low MPKI Benchmark HMean"],
                    "Data": [harmonic_mean(low_mpki_benchmark_baseline["Data"])],
                }
            ),
            pd.DataFrame(
                {
                    "Benchmark": ["High MPKI Benchmark HMean"],
                    "Data": [harmonic_mean(high_mpki_benchmark_baseline["Data"])],
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
                    "Benchmark": ["Low MPKI Benchmark HMean"],
                    "Data": [harmonic_mean(low_mpki_benchmark_Opt1["Data"])],
                }
            ),
            pd.DataFrame(
                {
                    "Benchmark": ["High MPKI Benchmark HMean"],
                    "Data": [harmonic_mean(high_mpki_benchmark_Opt1["Data"])],
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
                    "Benchmark": ["Low MPKI Benchmark HMean"],
                    "Data": [harmonic_mean(low_mpki_benchmark_Opt2["Data"])],
                }
            ),
            pd.DataFrame(
                {
                    "Benchmark": ["High MPKI Benchmark HMean"],
                    "Data": [harmonic_mean(high_mpki_benchmark_Opt2["Data"])],
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
                    "Benchmark": ["Low MPKI Benchmark HMean"],
                    "Data": [harmonic_mean(low_mpki_benchmark_Opt3["Data"])],
                }
            ),
            pd.DataFrame(
                {
                    "Benchmark": ["High MPKI Benchmark HMean"],
                    "Data": [harmonic_mean(high_mpki_benchmark_Opt3["Data"])],
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
            "NBWalker": Opt1["Data"],
            "NBWalker+AMR": Opt2["Data"],
            "Infinite-Walker": Opt3["Data"],
        }
    )

    output_path = os.path.join(output_dir, "figure_12.csv")
    output_df.to_csv(output_path, index=False)

    print(f"Normalized performance data saved to: {output_path}")
    
    # ---------------------------------------------------------------
    # Build x-positions with a gap between the two groups
    # ---------------------------------------------------------------
    plt.rcParams["font.family"] = "Arial"
    plt.rcParams["font.sans-serif"] = ["Arial"]
    plt.rcParams["mathtext.fontset"] = "custom"
    plt.rcParams["mathtext.rm"] = "Arial"
    plt.rcParams["mathtext.it"] = "Arial:italic"
    plt.rcParams["mathtext.bf"] = "Arial:bold"

    plt.figure(figsize=(20, 3.5), dpi=300)

    low_mpki_benchmarks = get_low_mpki_benchmarks()
    high_mpki_benchmarks = get_high_mpki_benchmarks()
    
    bar_width   = 0.1
    group_step  = 4 * bar_width + 0.15   # width of one benchmark cluster
    gap         = 0.6                  # extra gap between high-MPKI and low-MPKI sections

    n_hmpki = len(high_mpki_benchmarks) + 1   # benchmarks + Ave.
    n_lmpki = len(low_mpki_benchmarks)  + 1

    # Positions for the leftmost bar of each cluster
    r1_hmpki = np.arange(n_hmpki) * group_step
    r1_lmpki = np.arange(n_lmpki) * group_step + r1_hmpki[-1] + group_step + gap

    r1 = np.concatenate([r1_hmpki, r1_lmpki])
    r2 = r1 + bar_width
    r3 = r2 + bar_width
    r4 = r3 + bar_width

    def build_full_plot_df(df: pd.DataFrame, low_benchmarks: list[str], high_benchmarks: list[str]) -> pd.DataFrame:
        high_df = df[df["Benchmark"].isin(high_benchmarks)]
        low_df = df[df["Benchmark"].isin(low_benchmarks)]

        return pd.concat(
            [
                high_df,
                pd.DataFrame(
                    {
                        "Benchmark": ["High MPKI Benchmark HMean"],
                        "Data": [harmonic_mean(high_df["Data"])],
                    }
                ),
                low_df,
                pd.DataFrame(
                    {
                        "Benchmark": ["Low MPKI Benchmark HMean"],
                        "Data": [harmonic_mean(low_df["Data"])],
                    }
                ),
            ],
            ignore_index=True,
        )

    # ---------------------------------------------------------------
    # Draw bars
    # ---------------------------------------------------------------
    baseline_full = build_full_plot_df(baseline, low_mpki_benchmarks, high_mpki_benchmarks)
    Opt1_full     = build_full_plot_df(Opt1, low_mpki_benchmarks, high_mpki_benchmarks)
    Opt2_full     = build_full_plot_df(Opt2, low_mpki_benchmarks, high_mpki_benchmarks)
    Opt3_full     = build_full_plot_df(Opt3, low_mpki_benchmarks, high_mpki_benchmarks)
    
    bar1 = plt.bar(r1, baseline_full["Data"], width=bar_width, label="baseline",
                   color="#8D2E2C", edgecolor="black", linewidth=1.5)
    bar2 = plt.bar(r2, Opt1_full["Data"],     width=bar_width, label="NB-Walker",
                   color="#C3D9F1", edgecolor="black", linewidth=1.5)
    bar3 = plt.bar(r3, Opt2_full["Data"],     width=bar_width, label="NB-Walker + AMR",
                   color="#5D73A1", edgecolor="black", linewidth=1.5)
    bar4 = plt.bar(r4, Opt3_full["Data"],     width=bar_width, label="infinite walkers",
                   color="#313A5B", edgecolor="black", linewidth=1.5)

    # Annotate bars that exceed the y-axis limit
    for bar in bar1 + bar2 + bar3 + bar4:
        height = bar.get_height()
        x = bar.get_x() + bar.get_width() / 2
        if height >= 4:
            if bar in bar2:
                plt.annotate(
                    f"{height:.2f}",
                    xy=(x, 2.3),  # 柱顶位置
                    xytext=(-35, 0),  # 相对偏移 (0,15) 表示向上15pt
                    textcoords="offset points",
                    ha="center",
                    va="bottom",
                    fontsize=20,
                    fontweight="bold",
                    bbox=dict(
                        facecolor="white",
                        edgecolor="black",
                        boxstyle="round,pad=0.1",
                    ),
                    arrowprops=dict(arrowstyle="-", color="red", lw=2),
                )
            elif bar in bar3:
                plt.annotate(
                    f"{height:.2f}",
                    xy=(x, 2.9),  # 柱顶位置
                    xytext=(-51, 0),  # 相对偏移 (0,15) 表示向上15pt
                    textcoords="offset points",
                    ha="center",
                    va="bottom",
                    fontsize=20,
                    fontweight="bold",
                    bbox=dict(
                        facecolor="white",
                        edgecolor="black",
                        boxstyle="round,pad=0.1",
                    ),
                    arrowprops=dict(arrowstyle="-", color="red", lw=2),
                )
            elif bar in bar4:
                plt.annotate(
                    f"{height:.2f}",
                    xy=(x, 3.5),  # 柱顶位置
                    xytext=(-67, 0),  # 相对偏移 (0,15) 表示向上15pt
                    textcoords="offset points",
                    ha="center",
                    va="bottom",
                    fontsize=20,
                    fontweight="bold",
                    bbox=dict(
                        facecolor="white",
                        edgecolor="black",
                        boxstyle="round,pad=0.1",
                    ),
                    arrowprops=dict(arrowstyle="-", color="red", lw=2),
                )

    # ---------------------------------------------------------------
    # X-tick labels
    # ---------------------------------------------------------------
    xtick_labels = (
        [get_short_name(b) for b in high_mpki_benchmarks] + ["HMean"] +
        [get_short_name(b) for b in low_mpki_benchmarks]  + ["HMean"]
    )
    xtick_positions = [r + 1.5 * bar_width for r in r1]

    plt.xlim(min(r1) - bar_width, max(r4) + bar_width)
    plt.xticks(xtick_positions, xtick_labels, fontsize=20, fontweight="bold")

    plt.ylabel("Speedup", fontsize=20, fontweight="bold")
    plt.yticks(np.arange(0, 4.1, 1), fontsize=20, fontweight="bold")
    plt.ylim(0, 4)

    # ---------------------------------------------------------------
    # Vertical dashed separator between the two groups
    # ---------------------------------------------------------------
    sep_x = (r4[n_hmpki - 1] + r1[n_hmpki]) / 2   # midpoint in the gap
    plt.axvline(x=sep_x, color="black", linewidth=1.5, linestyle="--")

    # ---------------------------------------------------------------
    # Group bracket labels below the x-axis (like the reference image)
    # ---------------------------------------------------------------
    ax = plt.gca()

    # x-data range for each group (using bar outer edges)
    hmpki_x_left  = r1[0]
    hmpki_x_right = r4[n_hmpki - 1]
    lmpki_x_left  = r1[n_hmpki]
    lmpki_x_right = r4[-1]

    # Convert data coords to axes coords for the bracket
    x_total = max(r4) + bar_width - (min(r1) - bar_width)
    x_start = min(r1) - bar_width

    def to_axes_x(data_x):
        return (data_x - x_start) / x_total

    bracket_y      = -0.15   # in axes coordinates (below the plot)
    label_y        = -0.22

    for x_left, x_right, label in [
        (hmpki_x_left, hmpki_x_right, "High L3 TLB MPKI Workloads"),
        (lmpki_x_left, lmpki_x_right, "Low L3 TLB MPKI Workloads"),
    ]:
        ax_left  = to_axes_x(x_left  - bar_width * 0.5)
        ax_right = to_axes_x(x_right + bar_width * 0.5)
        ax_mid   = (ax_left + ax_right) / 2

        # Bracket line
        ax.annotate(
            "",
            xy=(ax_left,  bracket_y),
            xytext=(ax_right, bracket_y),
            xycoords="axes fraction",
            textcoords="axes fraction",
            arrowprops=dict(arrowstyle="-", color="black", lw=1.5),
        )
        # Left tick
        ax.annotate(
            "",
            xy=(ax_left,  bracket_y),
            xytext=(ax_left,  bracket_y + 0.02),
            xycoords="axes fraction",
            textcoords="axes fraction",
            arrowprops=dict(arrowstyle="-", color="black", lw=1.5),
        )
        # Right tick
        ax.annotate(
            "",
            xy=(ax_right, bracket_y),
            xytext=(ax_right, bracket_y + 0.02),
            xycoords="axes fraction",
            textcoords="axes fraction",
            arrowprops=dict(arrowstyle="-", color="black", lw=1.5),
        )
        # Label
        ax.text(
            ax_mid, label_y, label,
            ha="center", va="top",
            fontsize=20, fontweight="bold",
            transform=ax.transAxes,
        )

    # ---------------------------------------------------------------
    # Legend, grid, baseline line, borders
    # ---------------------------------------------------------------
    plt.legend(
        loc="upper center",
        ncol=4,
        bbox_to_anchor=(0.5, 1),
        bbox_transform=plt.gcf().transFigure,
        frameon=True,
        fancybox=True,
        framealpha=0.7,
        prop={"weight": "bold", "size": 18},
    )
    plt.tight_layout(rect=[0, 0, 1, 0.95])
    plt.subplots_adjust(bottom=0.22)   # make room for the group labels
    plt.grid(axis="y", alpha=0.3)
    plt.axhline(y=1, color="red", linewidth=0.8, linestyle="--")

    for spine in ax.spines.values():
        spine.set_linewidth(1.75)

    output_file = os.path.join(output_dir, "figure_12")
    plt.savefig(output_file + ".png")
    print(f"Plot saved to {output_file}")

if __name__ == "__main__":
    baseline = pd.DataFrame(columns=["Benchmark", "Data"])
    Opt1     = pd.DataFrame(columns=["Benchmark", "Data"])
    Opt2     = pd.DataFrame(columns=["Benchmark", "Data"])
    Opt3     = pd.DataFrame(columns=["Benchmark", "Data"])

    for benchmark in get_benchmarks():
        def append_row(df, benchmark, input_dir):
            perf_data = collect_performance_data(benchmark_name=benchmark, input_dir=input_dir)
            return pd.concat(
                [df, pd.DataFrame({"Benchmark": [benchmark], "Data": [perf_data]})],
                ignore_index=True,
            )

        baseline = append_row(baseline, benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "Baseline"))
        Opt1     = append_row(Opt1,     benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "NBWalker"))
        Opt2     = append_row(Opt2,     benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "NBWalker+AMR"))
        Opt3     = append_row(Opt3,     benchmark, os.path.join(os.environ["PROJECT_ROOT"], "results", "Infinite-Walker"))

    generate_output_csv(
        baseline=baseline.copy(),
        Opt1=Opt1.copy(),
        Opt2=Opt2.copy(),
        Opt3=Opt3.copy(),
    )